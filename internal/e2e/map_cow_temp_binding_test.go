package e2e

// A Map COW mutator's result bound to a TEMPORARY (#6227).
//
// `__map_cow_inplace` returns the receiver's own handle when the map is
// uniquely held, so before the COW-seam retain landed the mutator's result
// and the receiver's binding shared ONE refcount. Releasing both then
// over-released, and what that looked like depended on the spelling:
//
//	var m2 = m.insert(k, v); m = m2;   -> entries silently VANISHED (a 3-insert
//	                                      loop produced a map of length 1)
//	var (m2, ok) = m.without(k); m = m2; -> rc underflow, then SIGSEGV once the
//	                                        freed handle was recycled
//	var m2 = m.cleared(); m = m2;      -> rc underflow
//
// The direct form `m = m.insert(k, v)` was correct throughout, because the
// assignment's COW-aware dec-on-overwrite cancelled the shared count — and
// that is the form every other map test in the suite uses, which is why this
// went unnoticed. `without` surfaced it first only because its
// `(Map[K, V], boolean)` return CANNOT be spelled directly: the temporary is
// forced, so the delete path had no safe idiom to hide behind.
//
// So these programs deliberately route EVERY mutator through a temporary.
// The direct form is covered elsewhere (map_verbs, map_hash_seed); repeating
// it here would re-test the one shape that never broke.

import "testing"

// Every mutator through a temporary, on a map that grows. Exit 42 on success
// so the interpreter — which was right about all of this — runs it too.
const mapCowTempBindingProg = `
import "core/map";
import "std/i32";

struct Holder { m: Map[string, i32] }

function main(): i32 {
    // insert: 40 inserts past a cap-4 start, so the table grows several times.
    var m: Map[string, i32] = map_new(4);
    var i: i32 = 0;
    while (i < 40) {
        var m2 = m.insert("k" + i.to_string(), i);
        m = m2;
        i = i + 1;
    }
    if (m.len() != 40) { return 1; }
    var j: i32 = 0;
    while (j < 40) {
        if (m.get_or("k" + j.to_string(), 0 - 1) != j) { return 2; }
        j = j + 1;
    }

    // without: a sweep over the grown map — the original #6227 repro, which
    // SEGV'd on every compiled backend and was right under -interp.
    var d: i32 = 0;
    while (d < 20) {
        var (md, ok) = m.without("k" + d.to_string());
        if (!ok) { return 3; }
        m = md;
        d = d + 1;
    }
    if (m.len() != 20) { return 4; }
    if (m.has("k7")) { return 5; }
    if (m.get_or("k25", 0 - 1) != 25) { return 6; }
    // The survivors of the swap-with-last backfill are all still reachable.
    var s: i32 = 20;
    while (s < 40) {
        if (m.get_or("k" + s.to_string(), 0 - 1) != s) { return 7; }
        s = s + 1;
    }

    // cleared through a temporary.
    var c: Map[string, i32] = map_new(8);
    c = c.insert("a", 1);
    c = c.insert("b", 2);
    var cleared = c.cleared();
    c = cleared;
    if (c.len() != 0) { return 8; }
    c = c.insert("a", 9);
    if (c.len() != 1) { return 9; }
    if (c.get_or("a", 0 - 1) != 9) { return 10; }

    // A FIELD receiver, which aliases through the container rather than a
    // local: the in-place COW hands back the handle the struct still holds.
    var m0: Map[string, i32] = map_new(8);
    var h: Holder = Holder { m: m0 };
    var f: i32 = 0;
    while (f < 12) {
        var nm = h.m.insert("f" + f.to_string(), f);
        h = Holder { m: nm };
        f = f + 1;
    }
    if (h.m.len() != 12) { return 11; }
    if (h.m.get_or("f3", 0 - 1) != 3) { return 12; }

    // A TEMPORARY receiver owes no retain — its own rc=1 transfers to the
    // result — so this must not be over-retained into a permanent leak.
    var t: Map[i32, i32] = map_new(4);
    t = t.insert(1, 10);
    var chained = t.insert(2, 20).insert(3, 30);
    if (chained.len() != 3) { return 13; }
    if (chained.get_or(3, 0 - 1) != 30) { return 14; }

    return 42;
}
`

func TestMapCowTempBindingInterp(t *testing.T) {
	if got := runInterpExit(t, mapCowTempBindingProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapCowTempBindingX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapCowTempBindingProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestMapCowTempBindingWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapCowTempBindingProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestMapCowTempBindingArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapCowTempBindingProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

// The rc probe. `__rc_underflow_count()` is a compiled-backend probe — the
// interpreter does not implement it — so this leg is compiled-only. It is the
// direct measurement: every shape above ran clean on len/get assertions long
// before the handle was actually freed, so a suite that only checks values
// would go green again the moment the SEGV moved out of reach rather than
// when the over-release stopped.
const mapCowTempBindingRcProg = `
import "core/map";
import "std/i32";

function main(): i32 {
    var m: Map[string, i32] = map_new(64);
    var i: i32 = 0;
    while (i < 8) {
        var m2 = m.insert("k" + i.to_string(), i);
        m = m2;
        i = i + 1;
    }
    var d: i32 = 0;
    while (d < 4) {
        var (md, ok) = m.without("k" + d.to_string());
        m = md;
        d = d + 1;
    }
    var c = m.cleared();
    m = c;
    // 42 when clean; every underflow the run recorded adds to it.
    return 42 + __rc_underflow_count();
}
`

func TestMapCowTempBindingNoUnderflowX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapCowTempBindingRcProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (%d rc underflows)", got, got-42)
	}
}

func TestMapCowTempBindingNoUnderflowWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapCowTempBindingRcProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (%d rc underflows)", got, got-42)
	}
}

func TestMapCowTempBindingNoUnderflowArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapCowTempBindingRcProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (%d rc underflows)", got, got-42)
	}
}

// Memory: the temporary-bound loop must stay FLAT. The retain makes the
// result an owned reference, and an owned reference that nothing releases is
// a leak rather than a crash — so this is the half of the fix that value
// assertions cannot see. `m = m2` overwrites a Map local, and the flat
// __fern_rc_dec that used to serve that overwrite freed neither the buf nor
// the value column, so once correct refcounts made the COW actually copy,
// every iteration orphaned a whole table (measured: 1328 B per iteration,
// unbounded).
//
// __heap_bump_bytes() is the bump allocator's high-water mark — what the
// freelist could NOT recycle — so it is host-independent, unlike RSS (see
// CLAUDE.md on the THP 12x spread).
//
// x86-64 ONLY. arm64 leaks ~42 B per string-keyed insert in the DIRECT form
// too — measured on stock main at 4576 / 16352 / 66528 B for 100 / 400 / 1600
// iterations, identical before and after this change — so a flatness
// assertion there measures that pre-existing churn, not this one. The leak
// this pins is 1328 B an iteration, thirty times larger, and x86-64 shows it
// against a genuinely flat baseline.
const mapCowTempBindingHeapProg = `
import "core/map";
import "std/i32";

function fill(n: i32): i64 {
    var m: Map[string, i32] = map_new(64);
    var i: i32 = 0;
    while (i < n) {
        var m2 = m.insert("k" + (i % 32).to_string(), i);
        m = m2;
        i = i + 1;
    }
    if (m.len() != 32) { return 0 - 1; }
    return __heap_bump_bytes();
}

function main(): i32 {
    // Same map, 4x the iterations. A per-iteration leak shows up as growth;
    // recycled churn does not.
    var few: i64 = fill(100);
    var many: i64 = fill(400);
    if (few < 0) { return 1; }
    if (many > few) { return 2; }
    return 42;
}
`

func TestMapCowTempBindingHeapFlatX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapCowTempBindingHeapProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (2 = the temporary-bound insert loop leaks per iteration)", got)
	}
}
