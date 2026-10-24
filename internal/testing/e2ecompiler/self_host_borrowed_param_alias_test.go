package e2ecompiler

import (
	"os/exec"
	"testing"
)

const borrowedParamAliasSrc = `struct Box { v: i32, tag: string }
function step(b: Box): Box { return Box { v: b.v - 1, tag: "s" }; }
function walk(a: Box, b: Box): Box {
    let x: Box = a;
    let y: Box = b;
    while (y.v > 0) {
        let t: Box = step(y);
        x = y;
        y = t;
    }
    return x;
}
function main(): i32 {
    let n: Box = Box { v: 3, tag: "n" };
    let r: Box = walk(Box { v: 9, tag: "a" }, n);
    let filler: Box = Box { v: 7, tag: "f" };
    return n.v * 10 + r.v + filler.v;
}`

func runStrictIRExit(t *testing.T, src, name string) int {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(src))
	progBin := buildBin(t, gcc, dir, name, string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
	}
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode()
}

func runStrictIRExitArm64(t *testing.T, src, name string) int {
	t.Helper()
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")
	asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(src), "-target", "arm64-linux")
	progBin := buildBin(t, arm64gcc, dir, name, string(asm))
	cmd := runArm64Bin(qemu, progBin)
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostBorrowedParamAliasX86_64(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	want := interpExit(t, interpBin, borrowedParamAliasSrc)
	if got := runStrictIRExit(t, borrowedParamAliasSrc, "param_alias"); got != want {
		t.Errorf("exit = %d, want %d (interp oracle; 8 = the caller's box was freed by the rebind)", got, want)
	}
}

func TestSelfHostBorrowedParamAliasArm64(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	want := interpExit(t, interpBin, borrowedParamAliasSrc)
	if got := runStrictIRExitArm64(t, borrowedParamAliasSrc, "param_alias"); got != want {
		t.Errorf("exit = %d, want %d (interp oracle; 8 = the caller's box was freed by the rebind)", got, want)
	}
}
