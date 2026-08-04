package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMapVsReclaimIRX86_64 pins #4353 (cut 2): the register-backend map
// string-VALUE column deep-release. Before this, `__fern_map_free` freed the
// keys/vals buffers but the string VALUE boxes held in the vals buffer leaked one
// level. A reclaimable `Map[K, string]` whose every value is a fresh string is now
// credited "MAPVS:" and routed to `__fern_map_free_vs`, which frees the vals
// column via `__fern_str_arr_free` (each value box + the buffer).
//
// The flatness case is DIFFERENTIAL: it compares a string-valued map's heap growth
// against an i32-valued map of the same shape. Both share the map's `__fern_arr_push`
// grow-leak (the abandoned intermediate keys/vals buffers — a separate, documented
// LOAD-BEARING leak gated on the reuse-analysis routing), so that baseline cancels;
// only the VALUE column can make the string map grow MORE. With the deep-release the
// two grow equally. (A raw `b2-b1` exit code is avoided — it wraps mod 256 and the
// grow-leak's 96000 % 256 == 0 once masked this leak as "flat".) The map is built in
// a HELPER (freed at the callee's exit sweep every call) because a map declared
// directly in a loop is not freed per iteration (a separate pre-existing gap).
func TestSelfHostMapVsReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (1 = string values leak beyond the shared grow-leak; 88 = live value freed; 99 = over-release)", name, code, want)
		}
	}

	// Differential: a Map[i32, string] with fresh-concat values must not grow MORE
	// than a same-shape Map[i32, i32] — i.e. the value column adds no leak beyond
	// the shared arr_push grow-leak. Returns 1 iff the string map leaks its values.
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
    while (i < 200) { acc = acc + build_str(i) + build_i32(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build_str(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    var k: i32 = 0;
    while (k < 2000) { acc = acc + build_i32(k); k = k + 1; }
    var k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    var str_growth: i32 = s2 - s1;
    var i32_growth: i32 = k2 - s2;
    if (str_growth > i32_growth + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, "mapvs-value-column-flat", 0)

	// Value correctness through the churn — no premature free / over-release.
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var m: Map[i32, string] = Map { 7: "hel" + "lo", 8: "wor" + "ld" };
        if (m.get_or(7, "").len() != 5) { bad = 1; }
        if (m.get_or(8, "").len() != 5) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "mapvs-value-correct", 0)

	// ALIASED value exclusion: a value stored from a bare string LOCAL is not
	// fresh (str_local_binding_is_fresh is false for an ident), so the map — though
	// still MAP-reclaimable — is NOT credited MAPVS and keeps the shallow free. The
	// local `s` (its own reclaimed string) is thus not double-freed under the map.
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var s: string = "aa" + "bb";
        var m: Map[i32, string] = Map { 1: s };
        if (s.len() != 4) { bad = 1; }
        if (m.get_or(1, "").len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "mapvs-aliased-value-excluded", 0)
}
