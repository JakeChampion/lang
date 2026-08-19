package e2e

// A Map whose VALUE column is cell-boxed (#7114).
//
// A wide scalar (i64 / u64 / f64) does not fit the Map runtime's
// pointer-wide value slot, so emitWideMapSet alloc-and-stores it into a cell
// and the column carries cell pointers. Only the INSERT branch of
// __map_set_keyed_impl takes the incoming value; an overwrite displaced the
// cell already in the column and nothing reclaimed it — 16 B per overwrite,
// unbounded in a loop, on x86-64, arm64 AND wasm. Unlike the KEY-cell leak
// of #6243, cell-boxing a wide V is not two-word-ABI-specific, which is why
// the x86-64 leg carries signal here. Delete and clear displaced their cells
// the same way.
//
// The column is now OWNED: __map_own_copied_cols gives a COW copy cells of
// its own, which is what makes reclaiming a displaced one safe rather than a
// use-after-free. The alias program below is the other half of that claim.

import "testing"

// Memory: overwrite, delete and clear must all stay flat on every target.
//
// 16x the iterations rather than 4x: at 16 B an overwrite the pre-fix mark
// grew 2928 -> 28528 across this spread (x86-64; it is flat at 1856 after),
// so nothing subtle is being asked of the comparison.
//
// __heap_bump_bytes() is the bump allocator's high-water mark — what the
// freelist could NOT recycle — so it is host-independent, unlike RSS.
const mapWideValueOverwriteHeapProg = `
import "core/map";
import "std/i32";

function fillWide(n: i32): i64 {
    var m: Map[i32, i64] = map_new(64);
    var i: i32 = 0;
    while (i < n) {
        m = m.insert(i % 32, i as i64);
        i = i + 1;
    }
    if (m.len() != 32) { return 0 - 1; }
    return __heap_bump_bytes();
}

function fillFloat(n: i32): i64 {
    var m: Map[i32, f64] = map_new(64);
    var i: i32 = 0;
    while (i < n) {
        m = m.insert(i % 32, 1.5);
        i = i + 1;
    }
    if (m.len() != 32) { return 0 - 1; }
    return __heap_bump_bytes();
}

function churnDelete(n: i32): i64 {
    var m: Map[i32, i64] = map_new(64);
    var i: i32 = 0;
    while (i < n) {
        m = m.insert(7, i as i64);
        var (m2, ok) = m.without(7);
        if (!ok) { return 0 - 1; }
        m = m2;
        i = i + 1;
    }
    if (m.len() != 0) { return 0 - 2; }
    return __heap_bump_bytes();
}

function churnClear(n: i32): i64 {
    var m: Map[i32, i64] = map_new(64);
    var i: i32 = 0;
    while (i < n) {
        m = m.insert(1, i as i64);
        m = m.insert(2, i as i64);
        var c = m.cleared();
        m = c;
        i = i + 1;
    }
    if (m.len() != 0) { return 0 - 1; }
    return __heap_bump_bytes();
}

function main(): i32 {
    var w1: i64 = fillWide(100);
    var w2: i64 = fillWide(1600);
    if (w1 < 0) { return 1; }
    if (w2 > w1) { return 2; }
    var f1: i64 = fillFloat(100);
    var f2: i64 = fillFloat(1600);
    if (f1 < 0) { return 3; }
    if (f2 > f1) { return 4; }
    var d1: i64 = churnDelete(100);
    var d2: i64 = churnDelete(1600);
    if (d1 < 0) { return 5; }
    if (d2 > d1) { return 6; }
    var c1: i64 = churnClear(100);
    var c2: i64 = churnClear(1600);
    if (c1 < 0) { return 7; }
    if (c2 > c1) { return 8; }
    return 42;
}
`

func TestMapWideValueOverwriteHeapFlatX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapWideValueOverwriteHeapProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (2 = i64 value, 4 = f64 value, 6 = delete, 8 = clear leaks a cell per round)", got)
	}
}

func TestMapWideValueOverwriteHeapFlatArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapWideValueOverwriteHeapProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (2 = i64 value, 4 = f64 value, 6 = delete, 8 = clear leaks a cell per round)", got)
	}
}

func TestMapWideValueOverwriteHeapFlatWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapWideValueOverwriteHeapProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (2 = i64 value, 4 = f64 value, 6 = delete, 8 = clear leaks a cell per round)", got)
	}
}

// The displaced cell must be freed EXACTLY once, and only by the map that
// owns it. Reclaiming it caller-side — the shape #6243's key fix takes —
// is unsound here: a COW copy's value column shares the ORIGINAL's cells
// until __map_own_copied_cols reboxes them, and an overwrite in either map
// would then free a cell the other still reads. Both sharing paths are
// exercised (a temporary-bound mutator through __map_cow_inplace, and a
// container construction through __map_clone); each copy must read back the
// values it was built with after the original is overwritten many times
// over.
//
// Without the rebox this returns 3 on x86-64 and arm64 — the copy's entry
// reads a cell the original freed and the allocator handed back out.
const mapWideValueOverwriteAliasProg = `
import "core/map";
import "std/i32";

struct Holder { m: Map[i32, i64] }

function main(): i32 {
    var m: Map[i32, i64] = map_new(8);
    m = m.insert(1, 11 as i64);
    m = m.insert(2, 22 as i64);
    var m2 = m.insert(3, 33 as i64);
    var h = Holder { m: m.insert(4, 44 as i64) };
    var i: i32 = 0;
    while (i < 64) {
        m = m.insert(1, (100 + i) as i64);
        m = m.insert(2, (200 + i) as i64);
        i = i + 1;
    }
    if (m.get_or(1, 0 as i64) != 163 as i64) { return 1; }
    if (m.get_or(2, 0 as i64) != 263 as i64) { return 2; }
    if (m2.get_or(1, 0 as i64) != 11 as i64) { return 3; }
    if (m2.get_or(2, 0 as i64) != 22 as i64) { return 4; }
    if (m2.get_or(3, 0 as i64) != 33 as i64) { return 5; }
    if (h.m.get_or(1, 0 as i64) != 11 as i64) { return 6; }
    if (h.m.get_or(4, 0 as i64) != 44 as i64) { return 7; }
    return 42 + __rc_underflow_count();
}
`

func TestMapWideValueOverwriteAliasX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapWideValueOverwriteAliasProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (3/4/5 = a COW copy read a freed cell; 43 = over-release)", got)
	}
}

func TestMapWideValueOverwriteAliasArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapWideValueOverwriteAliasProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (3/4/5 = a COW copy read a freed cell; 43 = over-release)", got)
	}
}

func TestMapWideValueOverwriteAliasWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapWideValueOverwriteAliasProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (3/4/5 = a COW copy read a freed cell; 43 = over-release)", got)
	}
}

// Every read path over a boxed column copies the payload OUT of the cell
// rather than handing the pointer on — which is what keeps reclaiming a
// displaced cell invisible to a reader. get() reboxes into its own
// Option[V], get_or / iter unbox, and values() copies into a wide-stride
// V[]; so a value read before an overwrite must survive the overwrite that
// frees the cell it came from. A wide KEY column rides along: its cells are
// the key machinery's, and the value walk must not disturb them.
const mapWideValueReadAfterOverwriteProg = `
import "core/map";
import "std/i32";

function main(): i32 {
    var w: Map[i64, i64] = map_new(8);
    var i: i32 = 0;
    while (i < 64) {
        w = w.insert((i % 4) as i64, (i * 10) as i64);
        i = i + 1;
    }
    if (w.len() != 4) { return 1; }
    if (w.get_or(3 as i64, 0 as i64) != 630 as i64) { return 2; }

    var m: Map[i32, i64] = map_new(8);
    m = m.insert(1, 100 as i64);
    m = m.insert(2, 200 as i64);
    m = m.insert(1, 111 as i64);
    var vs: i64[] = m.values();
    if (vs.len() != 2) { return 3; }
    var sum: i64 = 0 as i64;
    var j: i32 = 0;
    while (j < vs.len()) {
        sum = sum + vs[j];
        j = j + 1;
    }
    if (sum != 311 as i64) { return 4; }

    var isum: i64 = 0 as i64;
    for (k, v) in m {
        isum = isum + v;
    }
    if (isum != 311 as i64) { return 5; }

    // The Option outlives the cell it was read from.
    var got = m.get(1);
    m = m.insert(1, 999 as i64);
    var gv: i64 = match (got) { Some(x) => x, None => 0 as i64 };
    if (gv != 111 as i64) { return 6; }
    if (m.get_or(1, 0 as i64) != 999 as i64) { return 7; }

    return 42 + __rc_underflow_count();
}
`

func TestMapWideValueReadAfterOverwriteX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapWideValueReadAfterOverwriteProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (43 = over-release)", got)
	}
}

func TestMapWideValueReadAfterOverwriteArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapWideValueReadAfterOverwriteProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (43 = over-release)", got)
	}
}

func TestMapWideValueReadAfterOverwriteWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapWideValueReadAfterOverwriteProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (43 = over-release)", got)
	}
}
