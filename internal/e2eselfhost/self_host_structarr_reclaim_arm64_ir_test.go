package e2eselfhost

import (
	"testing"
)

// TestSelfHostStructArrReclaimIRArm64 is the arm64 port of
// TestSelfHostStructArrReclaimIRX86_64 (#4355 struct-array element-box reclaim):
// a fresh, non-escaping `var g = [P { .. }, P { .. }]` frees its element struct
// boxes + outer buffer via the arm64 __fn___fern_arrarr_free (one rc-guarded
// arr_dec per element pointer, which is exactly a struct-box free), instead of
// the shallow outer-only dec that leaked every element box per iteration. No new
// runtime — reuses the existing helper. Lighter churn under qemu.
func TestSelfHostStructArrReclaimIRArm64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (98 = element boxes leaked; 99 = over-release; 88 = live value freed; 97 = corrupted)", name, code, want)
		}
	}

	// scalar-field struct[] churn — flat at detector zero.
	run(t, `struct P { x: i32, y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var g = [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }];
        acc = acc + g.len() + g[0].x;
        i = i + 1;
    }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 500) {
        var g2 = [P { x: j, y: j + 1 }, P { x: j + 2, y: j + 3 }];
        acc = acc + g2.len() + g2[1].y;
        j = j + 1;
    }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "structarr-scalar-flat-arm64", 0)

	// TRANSIENT ITERATION admitted — still flat.
	run(t, `struct P { x: i32, y: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var g = [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }];
        for p in g { acc = acc + p.x + p.y; }
        i = i + 1;
    }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 500) {
        var g2 = [P { x: j, y: j + 1 }, P { x: j + 2, y: j + 3 }];
        for p in g2 { acc = acc + p.x; }
        j = j + 1;
    }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "structarr-iter-flat-arm64", 0)

	// ELEMENT-ALIAS exclusion — q stays valid, no over-release.
	run(t, `struct P { x: i32, y: i32 }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var g = [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }];
        var q = g[1];
        if (q.x != i + 2) { bad = 1; }
        if (q.y != i + 3) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "structarr-elem-alias-safe-arm64", 0)
}
