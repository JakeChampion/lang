package e2eselfhost

import (
	"testing"
)

// TestSelfHostArrArrCallRetReclaimIRArm64 is the arm64 port of
// TestSelfHostArrArrCallRetReclaimIRX86_64 (#4355 slice 10): the "AAC:"
// call-init admission and the fn-scope single-sweep pin, through the
// asm_arm64_ir path (the arm64 __fn___fern_arrarr_free /
// __fn___fern_strarrarr_free bodies from slice 9). Lighter churn under qemu.
func TestSelfHostArrArrCallRetReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (98 = leaked; 99 = over-release; 88 = live value freed; 97 = corrupted)", name, code, want)
		}
	}

	// string[][] CALL-INIT churn — flat at detector zero.
	run(t, `function mk(i: i32): string[][] {
    return [["a" + "b"], ["c" + "d", "e" + "f"]];
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var g: string[][] = mk(i);
        acc = acc + g.len() + g[0][0].len();
        i = i + 1;
    }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 1000) {
        var g2: string[][] = mk(j);
        acc = acc + g2.len() + g2[1][1].len();
        j = j + 1;
    }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "arrarr-callret-str-flat-arm64", 0)

	// PARAM-EMBEDDING exclusion pin.
	run(t, `function mk2(s: string): string[][] {
    return [[s], ["c" + "d"]];
}
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var s1: string = "aa" + "bb";
        var g: string[][] = mk2(s1);
        if (g[0][0].len() != 4) { bad = 1; }
        if (s1.len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "arrarr-callret-param-embed-safe-arm64", 0)

	// FN-SCOPE single-sweep pin (the slice-9 double-sweep fix on arm64).
	run(t, `function work(n: i32): i32 {
    var g: string[][] = [["a" + "b"], ["c" + "d", "e" + "f"]];
    return g.len() + g[0][0].len() + n;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + work(i); i = i + 1; }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 1000) { acc = acc + work(j); j = j + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "arrarr-fnscope-sweep-flat-arm64", 0)
}
