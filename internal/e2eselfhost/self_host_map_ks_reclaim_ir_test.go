package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMapKsReclaimIRX86_64 pins #4353's string-KEY column (and both-column)
// deep-release — the sibling of the value-column slice. A reclaimable
// `Map[string, V]` whose every key is a fresh string is credited "MAPKS:" and
// routed to __fern_map_free_ks (keys column freed via __fern_str_arr_free); a
// `Map[string, string]` with fresh keys AND values routes to __fern_map_free_kvs
// (both columns). Flatness is DIFFERENTIAL against a LITERAL-keyed
// Map[string, i32] baseline so the shared arr_push grow-leak cancels: since
// #4353's owned grow, an i32-keyed map no longer shares that floor (i32/boolean
// values normalize to i32 in the type tag, so any i32-keyed scalar map is
// owncols and its grow frees the superseded buffers) — a string-keyed map with
// .rodata literal keys has the identical buffer shape and stays on the
// leak-only push. The maps are built-and-dropped without a lookup
// (m.has("a"+"b") would allocate a fresh lookup-key temp that leaks independently).
func TestSelfHostMapKsReclaimIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (1 = string keys/values leak beyond grow-leak; 88 = live value freed; 99 = over-release)", name, code, want)
		}
	}

	// string-KEY column: fresh-concat keys must not grow more than the
	// same-shape literal-keyed baseline (still on the leak-only grow, unlike
	// the now-flat i32-keyed owncols maps).
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
    while (i < 200) { acc = acc + build_sk(i) + build_ik(i); i = i + 1; }
    var s1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build_sk(j); j = j + 1; }
    var s2: i32 = __heap_bump_bytes();
    var k: i32 = 0;
    while (k < 2000) { acc = acc + build_ik(k); k = k + 1; }
    var k2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, "mapks-key-column-flat", 0)

	// BOTH columns: Map[string, string] with fresh keys AND values (→ _kvs).
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
    while (i < 200) { acc = acc + build_ss(i) + build_ii(i); i = i + 1; }
    var s1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build_ss(j); j = j + 1; }
    var s2: i32 = __heap_bump_bytes();
    var k: i32 = 0;
    while (k < 2000) { acc = acc + build_ii(k); k = k + 1; }
    var k2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, "mapkvs-both-columns-flat", 0)

	// Correctness: a string-keyed map reads back correctly (literal key lookup, no
	// fresh-temp confound) through the churn.
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var m: Map[string, i32] = Map { "hel" + "lo": 5, "wor" + "ld": 6 };
        if (m.get_or("hello", 0) != 5) { bad = 1; }
        if (m.get_or("world", 0) != 6) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "mapks-key-correct", 0)

	// ALIASED key exclusion: a key from a bare string LOCAL is not fresh, so the
	// map is NOT credited MAPKS and keeps the shallow key free — the local `s` is
	// not double-freed under the map.
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var s: string = "aa" + "bb";
        var m: Map[string, i32] = Map { s: 7 };
        if (s.len() != 4) { bad = 1; }
        if (m.get_or("aabb", 0) != 7) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "mapks-aliased-key-excluded", 0)

	// OVERWRITE with a recurring FRESH key (the word-count / histogram
	// m.set(computed_key, n) pattern): each re-insert of the same key overwrites
	// the slot and discards the incoming fresh key temp, which __fern_map_set now
	// frees (kconsume) rather than leaking. Differential against a literal-keyed
	// map doing the same overwrites (a .rodata literal key is guarded by
	// str_free, so nothing accrues), so the shared map buffer grow-leak
	// cancels. The register twin of the wasm overwrite fix.
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
    while (i < 200) { acc = acc + build_sk_over(i) + build_ik_over(i); i = i + 1; }
    var s1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build_sk_over(j); j = j + 1; }
    var s2: i32 = __heap_bump_bytes();
    var k: i32 = 0;
    while (k < 2000) { acc = acc + build_ik_over(k); k = k + 1; }
    var k2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, "mapks-overwrite-fresh-key-flat", 0)

	// Overwrite correctness + no over-release: the recurring fresh key reads back
	// the LAST value and len stays 1 through the churn.
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
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
}`, "mapks-overwrite-fresh-key-correct", 0)

	// Overwrite with an ALIASED key (a bare local reused across re-inserts):
	// kconsume=0, so map_set must NOT free the key — the local stays valid through
	// the overwrites and there is no over-release.
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
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
}`, "mapks-overwrite-aliased-key", 0)
}
