package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMapKeysSnapshotIRX86_64 pins #4353's coupled foundational change
// (slices 1+2, register x86-64): SCALAR (i32-element) map keys()/values() reads
// SNAPSHOT-COPY the column (__fern_map_snapshot_col) instead of aliasing the
// map's raw buffer, and — sound only together with that — an i32-key/i32-value
// map's __fern_map_set grow path frees the superseded buffer
// (__fern_arr_push_owned, the op_map_set `owncols` width bit), closing the
// cap-0 grow-leak (#4877). The three legs of the old mutually-compensating
// balance (keys() aliases the buffer / the ks result's exit-dec frees that
// alias / the map leaks its own buffers) are flipped together:
//   - `var ks = m.keys()` is an OWNED fresh copy: the exit sweep reclaims the
//     copy, later m.set mutations (including buffer-replacing grows) never
//     show through it, and the map's own buffers are freed by map_free /
//     the owned grow — no double free (__rc_underflow() == 0) and no leak.
//   - `for k in m.keys()` / `for (k, v) in m` scalar columns are snapshots
//     released right after the loop, so a body that mutates the map iterates
//     the entry-time snapshot (matching the wasm self-host backend) instead
//     of a buffer a growing insert may free from under it.
//
// String/pointer columns keep the historical alias + leak-only grow (slice 3),
// covered by TestSelfHostMapKsReclaimIRX86_64.
func TestSelfHostMapKeysSnapshotIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (1 = leak; 99 = over-release/double free; other = correctness step)", name, code, want)
		}
	}

	// SNAPSHOT SEMANTICS (the doc's differential-oracle case, matching the
	// native compiler): `var ks = m.keys()` must show the PRE-set length and
	// values after later inserts/overwrites — including inserts that GROW the
	// map (cap 4 -> 8), which now free the superseded buffer the old aliasing
	// read would have dangled on.
	run(t, `function main(): i32 {
    var m: Map[i32, i32] = Map { 1: 10, 2: 20 };
    var ks = m.keys();
    var vs: i32[] = m.values();
    m.set(9, 90);
    m.set(10, 100);
    m.set(11, 110);
    m.set(1, 11);
    if (ks.len() != 2) { return 10; }
    if (vs.len() != 2) { return 11; }
    var sv: i32 = 0;
    var i: i32 = 0;
    while (i < vs.len()) { sv = sv + vs[i]; i = i + 1; }
    if (sv != 30) { return 12; }
    if (m.len() != 5) { return 13; }
    if (m.get_or(1, 0) != 11) { return 14; }
    if (m.get_or(10, 0) != 100) { return 15; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, "map-keys-snapshot-semantics", 0)

	// GROW CHURN, no keys() taken: an i32/i32 map that grows twice per build
	// (len 13: cap 4 -> 8 -> 16) must be FLAT — the owned grow frees each
	// superseded buffer and map_free frees the final ones + the mapbox. This
	// is the #4877 grow-leak (48+ B/build) closing: before the coupled change
	// this churn grew by the abandoned cap-0/4/8 buffers every build.
	run(t, `function build(n: i32): i32 {
    var m: Map[i32, i32] = Map { 1: 2 };
    var j: i32 = 0;
    while (j < 12) { m.set(j + 10, j * 2); j = j + 1; }
    if (m.has(15)) { return m.len(); }
    return 0;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + build(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, "map-i32-grow-churn-flat", 0)

	// KEYS-TAKEN CHURN: build + grow + `var ks = m.keys()` per iteration must
	// also be flat — the exit sweep frees the snapshot COPY, map_free frees
	// the map's real buffers exactly once (no underflow), and the owned grow
	// frees the superseded ones. Under the old alias this shape double-dec'd
	// the live keys buffer (ks sweep + map_free) and leaked every grow.
	run(t, `function build(n: i32): i32 {
    var m: Map[i32, i32] = Map { 1: 2 };
    var j: i32 = 0;
    while (j < 8) { m.set(j + 10, j); j = j + 1; }
    var ks = m.keys();
    var vs = m.values();
    return ks.len() + vs.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + build(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, "map-keys-taken-churn-flat", 0)

	// MUTATION DURING `for (k, v) in m` + GROW: the loop iterates the
	// entry-time SNAPSHOT (3 entries), while the body's inserts push the map
	// through a grow (len 3 -> 6 crosses cap 4) whose owned free must not
	// dangle the iteration. Pins the self-host snapshot iteration semantics
	// (the wasm self-host backend has always snapshotted here; the native
	// runtime iterates the live map — a pre-existing, now-documented
	// divergence this change does not widen for the non-mutating case).
	run(t, `function main(): i32 {
    var m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30 };
    var total: i32 = 0;
    for (k, v) in m {
        total = total + k + v;
        m.set(k + 100, v);
    }
    if (total != 66) { return 50; }
    if (m.len() != 6) { return 51; }
    if (m.get_or(102, 0) != 20) { return 52; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, "map-kv-iter-mutate-snapshot", 0)

	// `for (k, v) in m` CHURN: the two per-loop scalar column snapshots are
	// released right after the loop (they are hidden, non-swept locals), so
	// repeated iteration stays flat and does not over-release (the map's own
	// buffers are freed exactly once, by map_free).
	run(t, `function build(n: i32): i32 {
    var m: Map[i32, i32] = Map { 1: 2, 3: 4, 5: 6 };
    var t: i32 = 0;
    for (k, v) in m { t = t + k + v; }
    return t;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + build(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc != 2200 * 21) { return 98; }
    return 0;
}`, "map-kv-iter-churn-flat", 0)

	// `for k in m.keys()` with a BREAK: the post-loop snapshot release sits
	// after the loop's exit label, so a break still frees the copy — flat
	// churn, correct partial sum, no over-release.
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30 };
        var s: i32 = 0;
        for k in m.keys() { if (k == 2) { break; } s = s + k; }
        if (s != 1) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "map-keys-loop-break", 0)

	// MIXED map (i32 keys, string values): the keys column snapshots (scalar)
	// while the values column snapshots with retain + MAPVS deep-release — the
	// snapshot must not disturb that balance (no underflow, values intact).
	// Since #5335 this map is owncols too (both columns snapshot-kind), so the
	// set(3, ..) grow frees the superseded buffers under the live ks snapshot.
	run(t, `function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var m: Map[i32, string] = Map { 1: "a" + "b", 2: "c" + "d" };
        var ks = m.keys();
        m.set(3, "e" + "f");
        if (ks.len() != 2) { bad = 1; }
        if (m.get_or(2, "").len() != 2) { bad = 1; }
        if (m.len() != 3) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "map-mixed-keys-snapshot", 0)

	// #4353 SLICE 3 (string columns, register x86-64): `for k in m.keys()`
	// over a string-KEYED map takes a RETAINING snapshot
	// (__fern_map_snapshot_col_str, per-element rc-inc) released deep after
	// the loop (__fern_str_arr_free). Churn must be FLAT vs a no-iteration
	// baseline (the snapshot copy + its element incs are fully reclaimed),
	// and __rc_underflow()==0 proves the deep release never over-frees a
	// map-owned key string (rc-aware __fern_str_free decs at rc>1). Keys seen
	// (correctness).
	run(t, `function build_iter(n: i32): i32 {
    var m: Map[string, i32] = Map { "a" + "x": 1, "b" + "y": 2, "c" + "z": 3 };
    var seen: i32 = 0;
    for k in m.keys() { seen = seen + k.len(); }
    return seen;
}
function build_noiter(n: i32): i32 {
    var m: Map[string, i32] = Map { "a" + "x": 1, "b" + "y": 2, "c" + "z": 3 };
    return m.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + build_iter(i) + build_noiter(i); i = i + 1; }
    var s0: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build_iter(j); j = j + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var k: i32 = 0;
    while (k < 2000) { acc = acc + build_noiter(k); k = k + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s1 - s0) > (s2 - s1) + 8192) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, "map-string-keys-iter-churn-flat", 0)

	// String-VALUED map via `for (k, v) in m`: the value column snapshots +
	// retains + deep-releases. Same flatness + underflow contract.
	run(t, `function build_iter(n: i32): i32 {
    var m: Map[i32, string] = Map { 1: "a" + "a", 2: "b" + "b" };
    var seen: i32 = 0;
    for (k, v) in m { seen = seen + v.len(); }
    return seen;
}
function build_noiter(n: i32): i32 {
    var m: Map[i32, string] = Map { 1: "a" + "a", 2: "b" + "b" };
    return m.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + build_iter(i) + build_noiter(i); i = i + 1; }
    var s0: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build_iter(j); j = j + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var k: i32 = 0;
    while (k < 2000) { acc = acc + build_noiter(k); k = k + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if ((s1 - s0) > (s2 - s1) + 8192) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, "map-string-values-iter-churn-flat", 0)
}
