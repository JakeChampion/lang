package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const closeIoErrorSrc = `function code_of(o: Result[void, IoError]): i32 {
    match (o) {
        Err(e) => {
            match (e) {
                Other(c, msg, _) => { if (msg == "Bad file descriptor") { return 7; } return 5; },
                _ => { return 4; }
            }
        },
        Ok(_) => { return 1; }
    }
}
function main(): i32 {
    let w: Writer = stdout();
    let first: i32 = code_of(w.close());
    let second: i32 = code_of(w.close());
    return first * 10 + second;
}`

func TestSelfHostCloseIoErrorIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	want := interpExit(t, interpBin, closeIoErrorSrc)
	asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(closeIoErrorSrc))
	progBin := buildBin(t, gcc, dir, "close_io_error", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	cmd.Stdout = nil
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != want {
		t.Errorf("close reporting = %d, want %d (interp oracle; 11 = the failing close answered Ok)", got, want)
	}
}

func TestSelfHostCloseIoErrorIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	want := interpExit(t, interpBin, closeIoErrorSrc)
	asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(closeIoErrorSrc), "-target", "arm64-linux")
	progBin := buildBin(t, arm64gcc, dir, "close_io_error", string(asm))
	cmd := runArm64Bin(qemu, progBin)
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != want {
		t.Errorf("close reporting = %d, want %d (interp oracle; 11 = the failing close answered Ok)", got, want)
	}
}

func TestSelfHostCloseIoErrorWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping close IoError wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	want := interpExit(t, interpBin, closeIoErrorSrc)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(closeIoErrorSrc))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "close_io_error.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	rcmd := exec.Command("wasmtime", "run", watFile)
	rcmd.Stdout = nil
	_ = rcmd.Run()
	if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if got := rcmd.ProcessState.ExitCode(); got != want {
		t.Errorf("close reporting = %d, want %d (interp oracle; 11 = the failing close answered Ok)", got, want)
	}
}
