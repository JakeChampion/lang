package e2eselfhost

import (
	"testing"
)

// TestSelfHostMapVsReclaimIRArm64 is the arm64 port of
// TestSelfHostMapVsReclaimIRX86_64 (#4353 cut 2): the arm64 __fn___fern_map_free_vs
// runtime body frees the map's VALUES column via __fn___fern_str_arr_free. The
// flatness case is DIFFERENTIAL (string-map vs i32-map growth) so the shared
// arr_push grow-leak cancels. Lighter churn under qemu.
func TestSelfHostMapVsReclaimIRArm64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (1 = string values leak beyond grow-leak; 88 = live value freed; 99 = over-release)", name, code, want)
		}
	}

	// Differential flatness: string-map growth must not exceed i32-map growth.
	run(t, `function build_str(n: i32): i32 {
    var m: Map[i32, string] = Map { 1: "a" + "b", 2: "c" + "d" };
    var r: i32 = 0;
    if (m.has(1)) { r = r + 1; }
    if (m.has(2)) { r = r + 1; }
    return r;
}
function build_i32(n: i32): i32 {
    var m: Map[i32, i32] = Map { 1: 2, 3: 4 };
    var r: i32 = 0;
    if (m.has(1)) { r = r + 1; }
    if (m.has(3)) { r = r + 1; }
    return r;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + build_str(i) + build_i32(i); i = i + 1; }
    var s1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 500) { acc = acc + build_str(j); j = j + 1; }
    var s2: i32 = __heap_bump_bytes();
    var k: i32 = 0;
    while (k < 500) { acc = acc + build_i32(k); k = k + 1; }
    var k2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    var str_growth: i32 = s2 - s1;
    var i32_growth: i32 = k2 - s2;
    if (str_growth > i32_growth + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, "mapvs-value-column-flat-arm64", 0)

	// Value correctness through the churn.
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var m: Map[i32, string] = Map { 7: "hel" + "lo", 8: "wor" + "ld" };
        if (m.get_or(7, "").len() != 5) { bad = 1; }
        if (m.get_or(8, "").len() != 5) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "mapvs-value-correct-arm64", 0)

	// Aliased-value exclusion.
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var s: string = "aa" + "bb";
        var m: Map[i32, string] = Map { 1: s };
        if (s.len() != 4) { bad = 1; }
        if (m.get_or(1, "").len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "mapvs-aliased-value-excluded-arm64", 0)
}
