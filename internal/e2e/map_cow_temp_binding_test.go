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
// docs/LOCAL-DEV-LOOP.md on the THP 12x spread).
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

func TestMapCowTempBindingHeapFlatArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapCowTempBindingHeapProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (2 = the temporary-bound insert loop leaks per iteration)", got)
	}
}

func TestMapCowTempBindingHeapFlatWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapCowTempBindingHeapProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (2 = the temporary-bound insert loop leaks per iteration)", got)
	}
}

// Memory: a string-keyed INSERT loop that mostly OVERWRITES must stay flat on
// every target (#6243).
//
// `set` is the one Map method that keeps the boxed key cell the lowering
// allocates on the two-word string ABI (wasm32, and arm64 under
// TwoWordOverride) — but only when it inserts. On an overwrite
// __map_set_keyed_impl keeps the equal key already in the column and returns,
// so the cell and the one string reference it carries were dropped on the
// floor: 16 B per overwrite for an immortal literal key, 32 B when the key is
// a fresh heap string. Unbounded in a loop, and invisible on x86-64, which
// does not box string keys at all.
//
// Three key shapes, because they leaked by different amounts and through
// different boxing decisions:
//
//	built string key   — cell + string buffer (32 B/iter on arm64 and wasm)
//	literal string key — the cell alone (16 B/iter), which pins the leak to
//	                     the BOXING rather than to the string
//	i64 key            — boxed on wasm32 only (mapKeyKindTag 2, wide scalar on
//	                     a narrow-pointer target): 16 B/iter there, and the
//	                     same defect with no string involved
//
// 16x the iterations rather than 4x: at 32 B an iteration the pre-fix arm64
// mark grew 4576 -> 52576 across this spread, so nothing subtle is being
// asked of the comparison.
//
// __heap_bump_bytes() is the bump allocator's high-water mark — what the
// freelist could NOT recycle — so it is host-independent, unlike RSS.
const mapStringKeyInsertHeapProg = `
import "core/map";
import "std/i32";

function fillBuilt(n: i32): i64 {
    var m: Map[string, i32] = map_new(64);
    var i: i32 = 0;
    while (i < n) {
        m = m.insert("k" + (i % 32).to_string(), i);
        i = i + 1;
    }
    if (m.len() != 32) { return 0 - 1; }
    return __heap_bump_bytes();
}

function fillLiteral(n: i32): i64 {
    var m: Map[string, i32] = map_new(64);
    var i: i32 = 0;
    while (i < n) {
        m = m.insert("kfixed", i);
        i = i + 1;
    }
    if (m.len() != 1) { return 0 - 1; }
    return __heap_bump_bytes();
}

function fillWide(n: i32): i64 {
    var m: Map[i64, i32] = map_new(64);
    var i: i32 = 0;
    while (i < n) {
        m = m.insert((i % 32) as i64, i);
        i = i + 1;
    }
    if (m.len() != 32) { return 0 - 1; }
    return __heap_bump_bytes();
}

function main(): i32 {
    var b1: i64 = fillBuilt(100);
    var b2: i64 = fillBuilt(1600);
    if (b1 < 0) { return 1; }
    if (b2 > b1) { return 2; }
    var l1: i64 = fillLiteral(100);
    var l2: i64 = fillLiteral(1600);
    if (l1 < 0) { return 3; }
    if (l2 > l1) { return 4; }
    var w1: i64 = fillWide(100);
    var w2: i64 = fillWide(1600);
    if (w1 < 0) { return 5; }
    if (w2 > w1) { return 6; }
    return 42;
}
`

func TestMapStringKeyInsertHeapFlatArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapStringKeyInsertHeapProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (2 = built key, 4 = literal key, 6 = i64 key leaks per overwrite)", got)
	}
}

func TestMapStringKeyInsertHeapFlatWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapStringKeyInsertHeapProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (2 = built key, 4 = literal key, 6 = i64 key leaks per overwrite)", got)
	}
}

func TestMapStringKeyInsertHeapFlatX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapStringKeyInsertHeapProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (2 = built key, 4 = literal key, 6 = i64 key leaks per overwrite)", got)
	}
}

// The discarded key cell must be released EXACTLY once. Freeing it is only
// safe because the boxed key on the set path always carries one owned string
// reference — a fresh key moves its rc in, an aliased one is retained by the
// set's key retain — so an aliased key overwritten in a loop must survive
// every one of those releases and still read back.
const mapStringKeyOverwriteAliasProg = `
import "core/map";
import "std/i32";

function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    // Aliased heap key: the caller keeps using it after every overwrite.
    var k: string = "alpha" + "-beta-gamma-delta";
    var i: i32 = 0;
    while (i < 64) {
        m = m.insert(k, i);
        i = i + 1;
    }
    if (m.len() != 1) { return 1; }
    if (k.len() != 22) { return 2; }
    if (m.get_or(k, 0 - 1) != 63) { return 3; }
    // An equal literal must still find the entry, so the stored key is intact.
    if (m.get_or("alpha-beta-gamma-delta", 0 - 1) != 63) { return 4; }

    // Inline (SSO) and heap keys interleaved: an inline key's data word is
    // not a pointer, and its release must stay a no-op.
    var s: Map[string, i32] = map_new(4);
    var z: i32 = 0;
    while (z < 100) {
        s = s.insert("ab", z);
        s = s.insert("a-much-longer-heap-allocated-key", z);
        z = z + 1;
    }
    if (s.len() != 2) { return 5; }
    if (s.get_or("ab", 0 - 1) != 99) { return 6; }
    if (s.get_or("a-much-longer-heap-allocated-key", 0 - 1) != 99) { return 7; }

    // Wide keys, boxed on wasm32 and bare elsewhere.
    var w: Map[i64, i32] = map_new(8);
    var y: i32 = 0;
    while (y < 200) {
        w = w.insert((y % 8) as i64, y);
        y = y + 1;
    }
    if (w.len() != 8) { return 8; }
    if (w.get_or(3 as i64, 0 - 1) != 195) { return 9; }

    return 42 + __rc_underflow_count();
}
`

func TestMapStringKeyOverwriteAliasArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapStringKeyOverwriteAliasProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

func TestMapStringKeyOverwriteAliasWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapStringKeyOverwriteAliasProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestMapStringKeyOverwriteAliasX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapStringKeyOverwriteAliasProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

// Memory: a string-keyed LOOKUP loop must stay flat on every target.
//
// On the two-word string ABI — wasm32, and arm64 under TwoWordOverride — a
// string map key does not fit the runtime's pointer-wide key slot, so the
// lowering boxes it into a cell and passes the cell pointer through. `set`
// keeps that cell; every read method (get / has / get_or / delete) does not,
// and freeLookupKeyCell exists to reclaim it. It was gated on `ptrW != 4`,
// on the belief that boxing was wasm-only — but boxing keys off the two-word
// ABI, which arm64 also runs under. So on arm64 every string-keyed lookup
// allocated a 16-byte cell and freed nothing, plus another 16 when the key was
// a fresh temporary whose buffer also went unreleased: 32 B per iteration of a
// loop like this one, unbounded, and invisible on x86-64, which does not box
// (#6243).
//
// Both key shapes are exercised because they leaked independently: a literal
// key leaked only the cell, a built one leaked the cell AND the string.
// Measured on arm64-darwin before the fix — 16 B/iter and 32 B/iter
// respectively, exactly linear over n = 100 / 200 / 400 / 800; after it, 1360
// and 1424 bytes flat at every n.
//
// The x86-64 leg does not box string keys (isStringForBoxing is false without
// the two-word ABI), so it pins the flat baseline this must not disturb.
//
// __heap_bump_bytes() is the bump allocator's high-water mark — what the
// freelist could NOT recycle — so it is host-independent, unlike RSS.
const mapStringKeyLookupHeapProg = `
import "core/map";
import "std/i32";

function lookups(n: i32): i64 {
    var m: Map[string, i32] = map_new(64);
    m = m.insert("k7", 1);
    var sink: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        // Literal key: leaks the boxed cell alone.
        sink = sink + m.get_or("k7", 0);
        if (m.has("k7")) { sink = sink + 1; }
        // Built key: leaks the cell AND the string buffer behind it.
        sink = sink + m.get_or("k" + (i % 32).to_string(), 0);
        i = i + 1;
    }
    if (sink < 0) { return 0 - 1; }
    return __heap_bump_bytes();
}

function main(): i32 {
    // Same map, 8x the iterations. A per-iteration leak shows up as growth in
    // the cumulative high-water mark; recycled churn does not.
    var few: i64 = lookups(100);
    var many: i64 = lookups(800);
    if (few < 0) { return 1; }
    if (many > few) { return 2; }
    return 42;
}
`

func TestMapStringKeyLookupHeapFlatArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapStringKeyLookupHeapProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (2 = the string-keyed lookup loop leaks per iteration)", got)
	}
}

func TestMapStringKeyLookupHeapFlatWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapStringKeyLookupHeapProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (2 = the string-keyed lookup loop leaks per iteration)", got)
	}
}

func TestMapStringKeyLookupHeapFlatX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapStringKeyLookupHeapProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (2 = the string-keyed lookup loop leaks per iteration)", got)
	}
}
