package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The target-OS compile-time constant through the SELF-HOST compiler (#8338).
//
// `__fern_target_os()` is substituted for a string literal by
// constfold.fold_target_os before anything reads the tree, and the folder
// beside it reduces the comparison — so what these cases prove is that the
// self-host answers for the TARGET it was given, on every emitter, and that
// the comparison resolved at compile time rather than reaching the runtime as
// a string compare against an undefined callee.
//
// The builtin is spelled directly rather than through `std/platform` because
// the driver harness feeds one source on stdin and never loads the stdlib;
// the std surface over it is gated natively (internal/e2e/target_os_test.go).
const targetOSSelfHostProg = `function main(): i32 {
    if (__fern_target_os() == "linux") { return 21; }
    if (__fern_target_os() == "wasi") { return 22; }
    if (__fern_target_os() == "darwin") { return 23; }
    return 24;
}`

const targetOSFailFmt = "%s: exit %d, want %d (24 = the builtin answered with an OS no case names; a crash means it reached codegen unsubstituted)"

func TestSelfHostTargetOSIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(targetOSSelfHostProg+"\n"))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	bin := buildBin(t, gcc, dir, "target-os", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 21 {
		t.Errorf(targetOSFailFmt, "x86-64-linux", code, 21)
	}
}

func TestSelfHostTargetOSIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(targetOSSelfHostProg+"\n"), "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "target-os", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 21 {
		t.Errorf(targetOSFailFmt, "arm64-linux", code, 21)
	}
}

func TestSelfHostTargetOSWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping target-OS wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(targetOSSelfHostProg + "\n"))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "target-os.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	rcmd := exec.Command("wasmtime", "run", watFile)
	_ = rcmd.Run()
	if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if code := rcmd.ProcessState.ExitCode(); code != 22 {
		t.Errorf(targetOSFailFmt, "wasm32-wasi", code, 22)
	}
}
