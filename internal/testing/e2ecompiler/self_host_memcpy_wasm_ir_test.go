package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostMemcpyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host memcpy wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	src := `function main(): i32 {
    let src: u8[] = __alloc_u8(3);
    src = src.with(0, 5 as u8); src = src.with(1, 7 as u8); src = src.with(2, 9 as u8);
    let dst: u8[] = __alloc_u8(3);
    __memcpy(dst as usize, src as usize, 20);
    return (dst[0] as i32) + (dst[1] as i32) + (dst[2] as i32);
}`
	const want = 21

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
	watFile := filepath.Join(dir, "memcpy_ir.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	rcmd := exec.Command("wasmtime", "run", watFile)
	_ = rcmd.Run()
	if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally (likely invalid WAT — broken memory.copy):\n%s", wat)
	}
	if got := rcmd.ProcessState.ExitCode(); got != want {
		t.Errorf("memcpy wasm IR exited %d, want %d", got, want)
	}
}
