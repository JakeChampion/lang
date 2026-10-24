package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
			"needs-an-output-path",
			[]string{"-target", "wasm32-wasi", "-emit", "command-module", entry},
			[]string{"command-module", "-o OUTPUT"},
		},
		{
			"native-target-is-refused",
			[]string{"-target", "x86-64-linux", "-emit", "command-module", "-o", dst, entry},
			[]string{"command-module", "x86-64-linux", "wasm32-wasi"},
		},
		{
			"the-http-reactor-has-no-main",
			[]string{"-target", "wasm32-wasi-http", "-emit", "command-module", "-o", dst, entry},
			[]string{"command-module", "wasm32-wasi-http"},
		},
		{
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
