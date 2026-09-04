package e2eselfhost

import (
	"os/exec"
	"testing"
)

// The comparator reads eight bytes at a time with a final load anchored at the
// END of each string, so it re-reads bytes the loop already covered whenever the
// length is not a multiple of eight (#8192). The interesting inputs are
// therefore the length classes around each word boundary and, within a length,
// the position of the single differing byte — a tail that skipped a byte, or a
// loop that stopped one word early, shows up at exactly one (length, position)
// pair and nowhere else.
//
// So this sweeps every length 0..24 against every differing position in it, plus
// the equal case and the off-by-one length at each size. `rep` builds each
// operand by concatenation, which yields a fresh box: literals are interned, and
// two names for one box would take the pointer-identity fast path without
// comparing anything.
const strEqLengthSweepSrc = `function rep(c: string, n: i32): string {
    var s: string = "";
    var i: i32 = 0;
    while (i < n) { s = s + c; i = i + 1; }
    return s;
}

function main(): i32 {
    var n: i32 = 0;
    while (n <= 24) {
        var a: string = rep("a", n);
        if (a.len() != n) { return 200; }
        if (!__fern_str_eq(a, rep("a", n))) { return 60 + n; }
        if (__fern_str_eq(a, rep("a", n + 1))) { return 100 + n; }
        var k: i32 = 0;
        while (k < n) {
            var b: string = rep("a", k) + "b" + rep("a", n - k - 1);
            if (b.len() != n) { return 201; }
            if (__fern_str_eq(a, b)) { return 150 + k; }
            k = k + 1;
        }
        n = n + 1;
    }
    return 42;
}
`

func strEqLengthSweepWant(t *testing.T, got int) {
	t.Helper()
	switch {
	case got == 42:
	case got == 200 || got == 201:
		t.Errorf("exited %d — `rep` built the wrong length, so the sweep tested nothing", got)
	case got >= 60 && got <= 84:
		t.Errorf("exited %d — two equal strings of length %d compared unequal", got, got-60)
	case got >= 100 && got <= 124:
		t.Errorf("exited %d — length %d compared equal to length %d", got, got-100, got-100+1)
	case got >= 150 && got <= 174:
		t.Errorf("exited %d — a difference at byte %d was not seen", got, got-150)
	default:
		t.Errorf("exited %d, want 42", got)
	}
}

// TestSelfHostStrEqLengthSweepX86_64 runs the sweep through the x86-64 emitter.
func TestSelfHostStrEqLengthSweepX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := string(runCapture(t, gcc, runner, driverBin, []byte(strEqLengthSweepSrc), "-ir"))
	if len(asm) == 0 {
		t.Fatal("self-host emitted 0 bytes")
	}
	bin := buildBin(t, gcc, dir, "streqlen", asm)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	_ = cmd.Run()
	strEqLengthSweepWant(t, cmd.ProcessState.ExitCode())
}

// TestSelfHostStrEqLengthSweepArm64 is the same sweep on arm64 under qemu: the
// helper body is shared, but each backend lowers it separately.
func TestSelfHostStrEqLengthSweepArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(strEqLengthSweepSrc), "-target", "arm64-linux"))
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "streqlen_arm64", asm)
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	strEqLengthSweepWant(t, cmd.ProcessState.ExitCode())
}
