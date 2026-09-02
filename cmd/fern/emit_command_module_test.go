package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Neither wasm output form the CLI had could report a program's exit code.
//
// The default `-target wasm32-wasi` composes a wasi:cli/run component, and that
// world's `run: func() -> result` carries ok or err and nothing wider, so
// `return 42` reaches the host as exit 1. `-emit core-module` has no `_start`
// at all — `wasmtime run` on one calls nothing and exits 0, which reads exactly
// like a program that ran and succeeded.
//
// A WASI preview-1 COMMAND is the shape that carries the value, and the shape
// `web/wasi-shim.js` runs, so it is what a browser needs to host a Fern program
// — or the self-host compiler — without a component transpile. It was reachable
// only from internal/wasm/playground, never from the CLI.

func TestEmitCommandModuleCarriesTheExitCode(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	bin := buildFernForStdoutTest(t)
	entry := writeFern(t, "function main(): i32 {\n  print(\"ran\");\n  return 42;\n}\n")
	out := filepath.Join(t.TempDir(), "cmd.wasm")

	if o, err := exec.Command(bin, "-target", "wasm32-wasi", "-emit", "command-module", "-o", out, entry).CombinedOutput(); err != nil {
		t.Fatalf("-emit command-module: %v\n%s", err, o)
	}
	run := exec.Command("wasmtime", "run", out)
	stdout, _ := run.Output()
	if got := run.ProcessState.ExitCode(); got != 42 {
		t.Errorf("exit %d, want 42 — main's return is the only value a command can report", got)
	}
	if got := strings.TrimSpace(string(stdout)); got != "ran" {
		t.Errorf("stdout %q, want %q", got, "ran")
	}
}

// The contrast that motivates the form: the same program as a core module runs
// nothing under `wasmtime run`, because a core module has no `_start`. Silent
// success is the worst possible failure mode for a playground, and it is what a
// host driving the wrong artifact gets.
func TestEmitCoreModuleHasNoCommandEntry(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	bin := buildFernForStdoutTest(t)
	entry := writeFern(t, "function main(): i32 {\n  print(\"ran\");\n  return 42;\n}\n")
	out := filepath.Join(t.TempDir(), "core.wasm")

	if o, err := exec.Command(bin, "-target", "wasm32-wasi", "-emit", "core-module", "-o", out, entry).CombinedOutput(); err != nil {
		t.Fatalf("-emit core-module: %v\n%s", err, o)
	}
	run := exec.Command("wasmtime", "run", out)
	stdout, _ := run.Output()
	if len(stdout) != 0 {
		t.Errorf("a core module has no _start, so `wasmtime run` should produce nothing; got %q", stdout)
	}
}

func TestEmitCommandModuleFlagErrors(t *testing.T) {
	bin := buildFernForStdoutTest(t)
	entry := writeFern(t, "function main(): i32 {\n  return 0;\n}\n")
	dst := filepath.Join(t.TempDir(), "out.wasm")

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{
			// The module is written to a path, so there is nothing to send to
			// stdout in its place.
			"needs-an-output-path",
			[]string{"-target", "wasm32-wasi", "-emit", "command-module", entry},
			[]string{"command-module", "-o OUTPUT"},
		},
		{
			// An output FORM belongs to the artifact, so it is refused rather
			// than ignored where the target cannot produce it.
			"native-target-is-refused",
			[]string{"-target", "x86-64-linux", "-emit", "command-module", "-o", dst, entry},
			[]string{"command-module", "x86-64-linux", "wasm32-wasi"},
		},
		{
			// wasm32-wasi-http exports a handler and has no main, so there is
			// nothing for a command entry to start.
			"the-http-reactor-has-no-main",
			[]string{"-target", "wasm32-wasi-http", "-emit", "command-module", "-o", dst, entry},
			[]string{"command-module", "wasm32-wasi-http"},
		},
		{
			// The refusal for an unknown form has to name both, or the new one
			// is undiscoverable from the error a typo produces.
			"unknown-form-names-both",
			[]string{"-target", "wasm32-wasi", "-emit", "commandmodule", "-o", dst, entry},
			[]string{"core-module", "command-module"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := exec.Command(bin, tc.args...).CombinedOutput()
			if err == nil {
				t.Fatalf("expected a refusal, got success:\n%s", out)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(out), want) {
					t.Errorf("error should mention %q:\n%s", want, out)
				}
			}
			if _, err := os.Stat(dst); err == nil {
				t.Error("a refused build must not leave an output file behind")
			}
		})
	}
}
