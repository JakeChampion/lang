package e2e

import "testing"

const mapCowTempBindingProg = `
import "core/map";
import "std/i32";

struct Holder { m: Map[string, i32] }

function main(): i32 {
    // insert: 40 inserts past a cap-4 start, so the table grows several times.
    let m: Map[string, i32] = map_new(4);
    let i: i32 = 0;
    while (i < 40) {
        let m2 = m.insert("k" + i.to_string(), i);
        m = m2;
        i = i + 1;
    }
    if (m.len() != 40) { return 1; }
    let j: i32 = 0;
    while (j < 40) {
        if (m.get_or("k" + j.to_string(), 0 - 1) != j) { return 2; }
        j = j + 1;
    }

    // without: a sweep over the grown map — the original #6227 repro, which
    // SEGV'd on every compiled backend and was right under -interp.
    let d: i32 = 0;
    while (d < 20) {
        let ok: boolean = m.contains("k" + d.to_string());
        let md = m.remove("k" + d.to_string());
        if (!ok) { return 3; }
        m = md;
        d = d + 1;
    }
    if (m.len() != 20) { return 4; }
    if (m.contains("k7")) { return 5; }
    if (m.get_or("k25", 0 - 1) != 25) { return 6; }
    // The survivors of the swap-with-last backfill are all still reachable.
    let s: i32 = 20;
    while (s < 40) {
        if (m.get_or("k" + s.to_string(), 0 - 1) != s) { return 7; }
        s = s + 1;
    }

    // cleared through a temporary.
    let c: Map[string, i32] = map_new(8);
    c = c.insert("a", 1);
    c = c.insert("b", 2);
    let cleared = c.cleared();
    c = cleared;
    if (c.len() != 0) { return 8; }
    c = c.insert("a", 9);
    if (c.len() != 1) { return 9; }
    if (c.get_or("a", 0 - 1) != 9) { return 10; }

    // A FIELD receiver, which aliases through the container rather than a
    // local: the in-place COW hands back the handle the struct still holds.
    let m0: Map[string, i32] = map_new(8);
    let h: Holder = Holder { m: m0 };
    let f: i32 = 0;
    while (f < 12) {
        let nm = h.m.insert("f" + f.to_string(), f);
        h = Holder { m: nm };
        f = f + 1;
    }
    if (h.m.len() != 12) { return 11; }
    if (h.m.get_or("f3", 0 - 1) != 3) { return 12; }

    // A TEMPORARY receiver owes no retain — its own rc=1 transfers to the
    // result — so this must not be over-retained into a permanent leak.
    let t: Map[i32, i32] = map_new(4);
    t = t.insert(1, 10);
    let chained = t.insert(2, 20).insert(3, 30);
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

const mapCowTempBindingRcProg = `
import "core/map";
import "std/i32";

function main(): i32 {
    let m: Map[string, i32] = map_new(64);
    let i: i32 = 0;
    while (i < 8) {
        let m2 = m.insert("k" + i.to_string(), i);
        m = m2;
        i = i + 1;
    }
    let d: i32 = 0;
    while (d < 4) {
        let ok: boolean = m.contains("k" + d.to_string());
        let md = m.remove("k" + d.to_string());
        m = md;
        d = d + 1;
    }
    let c = m.cleared();
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

const mapCowTempBindingHeapProg = `
import "core/map";
import "std/i32";

function fill(n: i32): i64 {
    let m: Map[string, i32] = map_new(64);
    let i: i32 = 0;
    while (i < n) {
        let m2 = m.insert("k" + (i % 32).to_string(), i);
        m = m2;
        i = i + 1;
    }
    if (m.len() != 32) { return 0 - 1; }
    return __heap_bump_bytes();
}

function main(): i32 {
    // Same map, 4x the iterations. A per-iteration leak shows up as growth;
    // recycled churn does not.
    let few: i64 = fill(100);
    let many: i64 = fill(400);
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

const mapStringKeyInsertHeapProg = `
import "core/map";
import "std/i32";

function fillBuilt(n: i32): i64 {
    let m: Map[string, i32] = map_new(64);
    let i: i32 = 0;
    while (i < n) {
        m = m.insert("k" + (i % 32).to_string(), i);
        i = i + 1;
    }
    if (m.len() != 32) { return 0 - 1; }
    return __heap_bump_bytes();
}

function fillLiteral(n: i32): i64 {
    let m: Map[string, i32] = map_new(64);
    let i: i32 = 0;
    while (i < n) {
        m = m.insert("kfixed", i);
        i = i + 1;
    }
    if (m.len() != 1) { return 0 - 1; }
    return __heap_bump_bytes();
}

function fillWide(n: i32): i64 {
    let m: Map[i64, i32] = map_new(64);
    let i: i32 = 0;
    while (i < n) {
        m = m.insert((i % 32) as i64, i);
        i = i + 1;
    }
    if (m.len() != 32) { return 0 - 1; }
    return __heap_bump_bytes();
}

function main(): i32 {
    let b1: i64 = fillBuilt(100);
    let b2: i64 = fillBuilt(1600);
    if (b1 < 0) { return 1; }
    if (b2 > b1) { return 2; }
    let l1: i64 = fillLiteral(100);
    let l2: i64 = fillLiteral(1600);
    if (l1 < 0) { return 3; }
    if (l2 > l1) { return 4; }
    let w1: i64 = fillWide(100);
    let w2: i64 = fillWide(1600);
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

const mapStringKeyOverwriteAliasProg = `
import "core/map";
import "std/i32";

function main(): i32 {
    let m: Map[string, i32] = map_new(8);
    // Aliased heap key: the caller keeps using it after every overwrite.
    let k: string = "alpha" + "-beta-gamma-delta";
    let i: i32 = 0;
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
    let s: Map[string, i32] = map_new(4);
    let z: i32 = 0;
    while (z < 100) {
        s = s.insert("ab", z);
        s = s.insert("a-much-longer-heap-allocated-key", z);
        z = z + 1;
    }
    if (s.len() != 2) { return 5; }
    if (s.get_or("ab", 0 - 1) != 99) { return 6; }
    if (s.get_or("a-much-longer-heap-allocated-key", 0 - 1) != 99) { return 7; }

    // Wide keys, boxed on wasm32 and bare elsewhere.
    let w: Map[i64, i32] = map_new(8);
    let y: i32 = 0;
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

const mapStringKeyLookupHeapProg = `
import "core/map";
import "std/i32";

function lookups(n: i32): i64 {
    let m: Map[string, i32] = map_new(64);
    m = m.insert("k7", 1);
    let sink: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        // Literal key: leaks the boxed cell alone.
        sink = sink + m.get_or("k7", 0);
        if (m.contains("k7")) { sink = sink + 1; }
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
    let few: i64 = lookups(100);
    let many: i64 = lookups(800);
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
