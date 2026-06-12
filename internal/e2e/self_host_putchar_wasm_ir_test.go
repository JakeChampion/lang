package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostPutcharWasmIR is the wasm leg of the self-host `putchar` IR
// support (issue #2839). The wasm AST backend already inlined putchar; this
// checks the IR path's op_putchar emission (wasm_ir.fern: stash the byte in a
// scratch local, point the scratch iovec at it, fd_write fd 1). The program is
// emitted via `-ir`, run under wasmtime, and the BYTES written to stdout are
// asserted ("Hi\n"), exit 0.
func TestSelfHostPutcharWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm putchar IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	src := "function main(): i32 { putchar(72); putchar(105); putchar(10); return 0; }\n"
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v (%d bytes wat)", err, len(wat))
	}
	watFile := filepath.Join(dir, "putchar.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	var stdout bytes.Buffer
	run.Stdout = &stdout
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("putchar program exited %d, want 0", code)
	}
	if got := stdout.String(); got != "Hi\n" {
		t.Errorf("putchar program wrote %q, want %q", got, "Hi\n")
	}
}
