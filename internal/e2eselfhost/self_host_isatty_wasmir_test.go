package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The self-host's wasm backend has to lower `isatty` too — `std/cli`'s colour
// gate calls it, and std/cli is stdlib, so "wasm rejects this builtin" would
// mean the stdlib does not compile for wasm.
//
// A component has no fd table, so the answer is the constant 0, matching what
// native's preview-2 helper emits (see internal/codegen/wasmbin's
// buildIsattyBodyP2). The test runs the module rather than grepping the WAT,
// because a wrong stack discipline — forgetting to drop the fd operand — is a
// validation error at instantiation, not a textual difference.
func TestSelfHostIsattyIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host isatty wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    if (isatty(1)) { return 7; }
    return 3;
}`
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "isatty.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 3 {
		t.Fatalf("exit = %d, want 3 (a component is not a terminal)\n%s", code, wat)
	}
}
