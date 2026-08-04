package e2eselfhost

import (
	"testing"
)

// TestSelfHostMapKsReclaimIRArm64 is the arm64 port of
// TestSelfHostMapKsReclaimIRX86_64 (#4353 string-KEY / both-column deep-release):
// the arm64 __fn___fern_map_free_ks / _kvs bodies free the keys (resp. both)
// column via __fn___fern_str_arr_free. Differential flatness against a
// LITERAL-keyed Map[string, i32] baseline (same buffer shape, still on the
// leak-only grow — an i32-keyed baseline no longer shares the grow floor since
// #4353's owned grow); lighter churn under qemu.
func TestSelfHostMapKsReclaimIRArm64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (1 = string keys/values leak; 88 = live value freed; 99 = over-release)", name, code, want)
		}
	}

	// string-KEY column differential.
	run(t, `function build_sk(n: i32): i32 {
    var m: Map[string, i32] = Map { "a" + "b": 1, "c" + "d": 2 };
    return 1;
}
function build_ik(n: i32): i32 {
    var m: Map[string, i32] = Map { "a": 1, "b": 2 };
    return 1;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + build_sk(i) + build_ik(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 500) { acc = acc + build_sk(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    var k: i32 = 0;
    while (k < 500) { acc = acc + build_ik(k); k = k + 1; }
    var k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, "mapks-key-column-flat-arm64", 0)

	// BOTH columns differential.
	run(t, `function build_ss(n: i32): i32 {
    var m: Map[string, string] = Map { "a" + "b": "x" + "y", "c" + "d": "z" + "w" };
    return 1;
}
function build_ii(n: i32): i32 {
    var m: Map[string, i32] = Map { "a": 1, "b": 2 };
    return 1;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + build_ss(i) + build_ii(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 500) { acc = acc + build_ss(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    var k: i32 = 0;
    while (k < 500) { acc = acc + build_ii(k); k = k + 1; }
    var k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, "mapkvs-both-columns-flat-arm64", 0)

	// Correctness + aliased-key exclusion.
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var m: Map[string, i32] = Map { "hel" + "lo": 5, "wor" + "ld": 6 };
        if (m.get_or("hello", 0) != 5) { bad = 1; }
        if (m.get_or("world", 0) != 6) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "mapks-key-correct-arm64", 0)

	// OVERWRITE with a recurring FRESH key (word-count / histogram
	// m.set(computed_key, n)): the arm64 __fern_map_set frees the discarded fresh
	// key (kconsume via x24) on an overwrite. Differential against a literal-keyed
	// map doing the same overwrites. Lighter churn under qemu.
	run(t, `function build_sk_over(n: i32): i32 {
    var m: Map[string, i32] = Map { "wo" + "rd": 0 };
    var j: i32 = 0;
    while (j < 8) { m.set("wo" + "rd", j); j = j + 1; }
    return 1;
}
function build_ik_over(n: i32): i32 {
    var m: Map[string, i32] = Map { "k": 0 };
    var j: i32 = 0;
    while (j < 8) { m.set("k", j); j = j + 1; }
    return 1;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { acc = acc + build_sk_over(i) + build_ik_over(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 500) { acc = acc + build_sk_over(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    var k: i32 = 0;
    while (k < 500) { acc = acc + build_ik_over(k); k = k + 1; }
    var k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, "mapks-overwrite-fresh-key-flat-arm64", 0)

	// Overwrite correctness + no over-release: recurring fresh key reads last value.
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var m: Map[string, i32] = Map { "wo" + "rd": 0 };
        var j: i32 = 0;
        while (j < 8) { m.set("wo" + "rd", j); j = j + 1; }
        if (m.get_or("word", 0) != 7) { bad = 1; }
        if (m.len() != 1) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "mapks-overwrite-fresh-key-correct-arm64", 0)

	// Overwrite with an ALIASED key (a bare local reused): kconsume=0, so map_set
	// must NOT free the key — the local stays valid, no over-release.
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var key: string = "wo" + "rd";
        var m: Map[string, i32] = Map { "wo" + "rd": 0 };
        var j: i32 = 0;
        while (j < 8) { m.set(key, j); j = j + 1; }
        if (key.len() != 4) { bad = 1; }
        if (m.get_or("word", 0) != 7) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "mapks-overwrite-aliased-key-arm64", 0)
}
