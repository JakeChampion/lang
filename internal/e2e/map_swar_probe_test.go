package e2e

// Coverage for core/map's SWAR group probe (#6198).
//
// The probe loops in __map_lookup_keyed / __map_set_keyed_impl /
// __map_delete_keyed_impl scan 8 ctrl bytes at a time and only touch the
// bucket column on an H2 tag match. Probe order is unchanged from the
// scalar linear probe, so this program pins the behaviours that the group
// rewrite could plausibly break and the older map tests do not target
// directly:
//
//   - the ctrl mirror at the minimum capacity (cap 4, where every group
//     load wraps and the one group covers the table twice);
//   - lookup/insert/delete of missing keys over tombstone runs (the probe
//     must stop at the first EMPTY, not at a tombstone, and must not cycle
//     in a table holding only full + tombstone buckets);
//   - tombstone reuse on re-insert (the ctrl byte must flip back from
//     tombstone to the new key's H2);
//   - growth with tombstones present (the rebuilt table starts all-empty
//     and re-derives every ctrl byte);
//   - the len<=8 entry-scan fast path boundary in both directions;
//   - churn at a size where probe chains cross group boundaries, for the
//     scalar (Wang mix), seeded string, and wide (i64) key kinds.
//
// Values follow arithmetic in the key, so verification needs no oracle
// container: presence and value are both recomputable per key.

import "testing"

const mapSwarProbeProg = `
import "core/map";
import "std/i32";

function main(): i32 {
    // Minimum capacity: cap 4, where the single ctrl group wraps the whole
    // table via the mirror, and the load factor keeps at most 2 live keys.
    var s: Map[i32, i32] = map_new(1);
    s = s.insert(10, 1);
    s = s.insert(20, 2);
    var d0: (Map[i32, i32], boolean) = s.without(10);
    if (!d0.1) { return 30; }
    s = d0.0;
    var d1: (Map[i32, i32], boolean) = s.without(99);   // missing: over a tombstone to empty
    if (d1.1) { return 31; }
    s = d1.0;
    s = s.insert(30, 3);                                 // tombstone reuse at cap 4
    if (s.has(10)) { return 32; }
    if (s.get_or(20, 0 - 1) != 2) { return 33; }
    if (s.get_or(30, 0 - 1) != 3) { return 34; }

    // len<=8 fast-path boundary: force the map over it, then back under it.
    var f: Map[i32, i32] = map_new(4);
    var fi: i32 = 0;
    while (fi < 12) { f = f.insert(fi * 100, fi); fi = fi + 1; }
    if (f.len() != 12) { return 40; }
    fi = 0;
    while (fi < 12) {
        if (f.get_or(fi * 100, 0 - 1) != fi) { return 41; }
        fi = fi + 1;
    }
    fi = 1;
    while (fi < 12) { f = f.without(fi * 100).0; fi = fi + 2; }   // back to 6 live
    if (f.len() != 6) { return 42 + 1; }
    fi = 0;
    while (fi < 12) {
        var wantHit: boolean = fi % 2 == 0;
        if (f.has(fi * 100) != wantHit) { return 44; }
        fi = fi + 1;
    }
    if (f.has(1234567)) { return 45; }

    // Churn at probing size: scalar (Wang-mixed i32) keys.
    var m: Map[i32, i32] = map_new(4);
    var n: i32 = 2000;
    var i: i32 = 0;
    while (i < n) { m = m.insert(i, i * 7 + 1); i = i + 1; }
    if (m.len() != n) { return 1; }
    i = 0;
    while (i < n) { m = m.without(i).0; i = i + 3; }
    i = 0;
    while (i < n) {
        if (i % 3 == 0) {
            if (m.has(i)) { return 2; }
            if (m.get_or(i, 0 - 1) != 0 - 1) { return 3; }
        } else {
            if (m.get_or(i, 0 - 1) != i * 7 + 1) { return 4; }
        }
        i = i + 1;
    }
    // Re-insert a third of the deleted keys over their tombstones…
    i = 0;
    while (i < n) { m = m.insert(i, i * 11 + 5); i = i + 6; }
    // …then extend past the load factor so growth runs with tombstones live.
    i = n;
    while (i < 2 * n) { m = m.insert(i, i * 7 + 1); i = i + 1; }
    i = 0;
    while (i < 2 * n) {
        var expect: i32 = i * 7 + 1;
        var absent: boolean = false;
        if (i < n && i % 3 == 0) {
            if (i % 6 == 0) { expect = i * 11 + 5; } else { absent = true; }
        }
        if (absent) {
            if (m.has(i)) { return 5; }
        } else {
            if (m.get_or(i, 0 - 1) != expect) { return 6; }
        }
        i = i + 1;
    }
    // Overwrite live keys in place: len must hold still.
    var lenBefore: i32 = m.len();
    i = 0;
    while (i < 100) { m = m.insert(n + i, 999 + i); i = i + 1; }
    if (m.len() != lenBefore) { return 7; }
    i = 0;
    while (i < 100) {
        if (m.get_or(n + i, 0 - 1) != 999 + i) { return 8; }
        i = i + 1;
    }

    // Seeded string keys: the H2 tag must come off the same seeded hash at
    // insert, lookup, delete, and grow time.
    var sm: Map[string, i32] = map_new(4);
    var sn: i32 = 600;
    var si: i32 = 0;
    while (si < sn) { sm = sm.insert("k" + si.to_string(), si * 3); si = si + 1; }
    if (sm.len() != sn) { return 9; }
    si = 1;
    while (si < sn) { sm = sm.without("k" + si.to_string()).0; si = si + 2; }
    si = 0;
    while (si < sn) {
        if (si % 2 == 0) {
            if (sm.get_or("k" + si.to_string(), 0 - 1) != si * 3) { return 10; }
        } else {
            if (sm.has("k" + si.to_string())) { return 11; }
        }
        si = si + 1;
    }
    if (sm.has("absent")) { return 12; }

    // Wide (i64) keys, hashed over both halves of the loaded word.
    var wm: Map[i64, i32] = map_new(4);
    var wn: i32 = 300;
    var wi: i32 = 0;
    while (wi < wn) {
        var wk: i64 = (wi as i64) * (4294967311 as i64);   // spills into the high word
        wm = wm.insert(wk, wi);
        wi = wi + 1;
    }
    if (wm.len() != wn) { return 13; }
    wi = 0;
    while (wi < wn) { wm = wm.without((wi as i64) * (4294967311 as i64)).0; wi = wi + 4; }
    wi = 0;
    while (wi < wn) {
        var wk2: i64 = (wi as i64) * (4294967311 as i64);
        if (wi % 4 == 0) {
            if (wm.has(wk2)) { return 14; }
        } else {
            if (wm.get_or(wk2, 0 - 1) != wi) { return 15; }
        }
        wi = wi + 1;
    }

    return 42;
}
`

func TestMapSwarProbeInterp(t *testing.T) {
	if got := runInterpExit(t, mapSwarProbeProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapSwarProbeX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapSwarProbeProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestMapSwarProbeWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapSwarProbeProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestMapSwarProbeArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapSwarProbeProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
