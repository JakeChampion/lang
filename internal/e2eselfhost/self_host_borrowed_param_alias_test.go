package e2eselfhost

import (
	"os/exec"
	"testing"
)

// A struct local bound from a BORROWED param records that param's box as the
// snapshot its rebinds must not free. `x = y` then stores a SECOND borrow the
// snapshot cannot name, and the next rebind released it.
//
// gcd is the shape that found it, in coreutils/factor.fern:
//
//	var x: BigInt = a;
//	var y: BigInt = b;
//	while (!y.is_zero()) { var t = bi_mod(x, y); x = y; y = t; }
//
// The second iteration's `x = y` freed the CALLER's number under it, so factor
// printed a 2 for an odd input and its rho search never converged — twelve
// rows of TestSelfHostCoreutilsParity/factor, including the ones that read as
// hangs.

// borrowedParamAliasSrc is that loop over a scalar struct, so the answer is
// readable: `walk` threads two borrowed params through `x = y`, and the
// caller's `n` has to survive it. 3*10 + 1 + 7 = 38; a released `n` reads 0 out
// of the block the next allocation hands out and the sum collapses to 8.
const borrowedParamAliasSrc = `struct Box { v: i32, tag: string }
function step(b: Box): Box { return Box { v: b.v - 1, tag: "s" }; }
function walk(a: Box, b: Box): Box {
    var x: Box = a;
    var y: Box = b;
    while (y.v > 0) {
        var t: Box = step(y);
        x = y;
        y = t;
    }
    return x;
}
function main(): i32 {
    var n: Box = Box { v: 3, tag: "n" };
    var r: Box = walk(Box { v: 9, tag: "a" }, n);
    var filler: Box = Box { v: 7, tag: "f" };
    return n.v * 10 + r.v + filler.v;
}`

// runStrictIRExit compiles `src` through the self-host IR driver for the host
// and returns the program's exit code.
func runStrictIRExit(t *testing.T, src, name string) int {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(src), "-ir")
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

// runStrictIRExitArm64 is the same program on the arm64 IR path, under qemu.
func runStrictIRExitArm64(t *testing.T, src, name string) int {
	t.Helper()
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(src), "-target", "arm64-linux", "-ir")
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
