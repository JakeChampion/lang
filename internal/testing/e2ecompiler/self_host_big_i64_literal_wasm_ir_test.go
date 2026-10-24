package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostBigI64LiteralWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host big-i64-literal wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	src := `function main(): i32 {
    let big: u64 = 9000000000000000007 as u64;
    return (big % 256 as u64) as i32;
}`
	want := interpExit(t, interpBin, src)

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "ir_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	rcmd := exec.Command("wasmtime", "run", watFile)
	_ = rcmd.Run()
	if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally (likely invalid WAT — the bug):\n%s", wat)
	}
	if got := rcmd.ProcessState.ExitCode(); got != want {
		t.Errorf("big-i64-literal wasm IR exited %d, want %d (interp oracle)", got, want)
	}
}
