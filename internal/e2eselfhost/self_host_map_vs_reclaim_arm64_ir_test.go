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
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := cli.emit(t, "arm64-linux", "import \"core/map\";\n"+prog)
		if code, _ := runArm64(t, gcc, qemu, asm); code != want {
			t.Errorf("%s exited %d, want %d (1 = string values leak beyond grow-leak; 88 = live value freed; 99 = over-release)", name, code, want)
		}
	}

	// Differential flatness: string-map growth must not exceed i32-map growth.
	run(t, `function build_str(n: i32): i32 {
    let m: Map[i32, string] = Map { 1: "a" + "b", 2: "c" + "d" };
    let r: i32 = 0;
    if (m.has(1)) { r = r + 1; }
    if (m.has(2)) { r = r + 1; }
    return r;
}
function build_i32(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: 2, 3: 4 };
    let r: i32 = 0;
    if (m.has(1)) { r = r + 1; }
    if (m.has(3)) { r = r + 1; }
    return r;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + build_str(i) + build_i32(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 500) { acc = acc + build_str(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 500) { acc = acc + build_i32(k); k = k + 1; }
    let k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    let str_growth: i32 = s2 - s1;
    let i32_growth: i32 = k2 - s2;
    if (str_growth > i32_growth + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, "mapvs-value-column-flat-arm64", 0)

	// Value correctness through the churn.
	run(t, `function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let m: Map[i32, string] = Map { 7: "hel" + "lo", 8: "wor" + "ld" };
        if (m.get_or(7, "").len() != 5) { bad = 1; }
        if (m.get_or(8, "").len() != 5) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "mapvs-value-correct-arm64", 0)

	// Aliased-value exclusion.
	run(t, `function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let s: string = "aa" + "bb";
        let m: Map[i32, string] = Map { 1: s };
        if (s.len() != 4) { bad = 1; }
        if (m.get_or(1, "").len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "mapvs-aliased-value-excluded-arm64", 0)
}
