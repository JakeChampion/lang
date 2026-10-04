package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// `-emit command-module` is native's name for a WASI preview-1 command: the
// core module plus a `_start` that runs main and exits with its value. The
// self-host's core module is already that shape, so the spelling produces the
// same bytes as `-emit core-module`, `wasmtime run` exits with main's value,
// and the form is refused for the other wasm target as native refuses it
// (#11408).
func TestSelfHostEmitCommandModule(t *testing.T) {
	cli := newStrictCLI(t)
	wasmtime := e2eharness.Wasmtime(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 7; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func(form, target, out string) ([]byte, error) {
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", target, "-emit", form, "-o", out, src, cli.stdlib)
		cmd.Env = childEnv()
		return cmd.CombinedOutput()
	}
	command := filepath.Join(dir, "command.wasm")
	if out, err := build("command-module", "wasm32-wasi", command); err != nil {
		t.Fatalf("-emit command-module: %v\n%s", err, out)
	}
	core := filepath.Join(dir, "core.wasm")
	if out, err := build("core-module", "wasm32-wasi", core); err != nil {
		t.Fatalf("-emit core-module: %v\n%s", err, out)
	}
	a, _ := os.ReadFile(command)
	b, _ := os.ReadFile(core)
	if len(a) == 0 || !bytes.Equal(a, b) {
		t.Errorf("command-module (%d bytes) and core-module (%d bytes) differ; the self-host's core is the command", len(a), len(b))
	}
	run := exec.Command(wasmtime, "run", command)
	var stderr bytes.Buffer
	run.Stderr = &stderr
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 7 {
		t.Errorf("wasmtime run exited %d, want main's 7\n%s", code, stderr.String())
	}
	if out, err := build("command-module", "wasm32-wasi-http", filepath.Join(dir, "http.wasm")); err == nil || !bytes.Contains(out, []byte("not available for -target wasm32-wasi-http")) {
		t.Errorf("-emit command-module for wasm32-wasi-http: err=%v, want the refusal native gives\n%s", err, out)
	}
}
