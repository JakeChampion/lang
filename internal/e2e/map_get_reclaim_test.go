package e2e

// `match (m.get(k))` releases the reference the lookup retained (#7144).
//
// `m.get(k)` hands the caller a COUNTED reference to the map's value —
// __map_retain_val for the array / deep-droppable kinds, a string retain
// emitMapGetRebox emits itself. The match-scrutinee reclaim freed only the
// rebuilt Option box and left that count outstanding, so the map's own column
// drop took the value from 2 to 1 and its storage was stranded: one value per
// lookup, unbounded in a loop. Measured per round over a 1000/2000-round churn
// (__heap_bump_bytes() delta / rounds), as x86-64 | arm64 | wasm:
//
//	Map[i32, i32[]], statement match   32 | 32 | 32
//	Map[i32, i32[]], expression match  32 | 32 | 32
//	Map[i32, Q]                        64 | 64 | 48
//	Map[i32, Option[i32]]              16 | 16 | 16
//	Map[i32, (i32, i32)]               16 | 16 | 16
//	Map[string, i32[]]                 32 | 32 | 32
//	Map[i32, Q[]]                      96 | 96 | 80
//
// All seven are flat afterwards on all three backends. The same churn WITHOUT
// the lookup was already 0, which is what identifies the lookup rather than the
// map (#7143 made the map itself flat).
//
// A string VALUE column has a residual of its own on x86-64 — kind 1 is not
// reclaimed by the map's drop at all, 64 B/round with or without a lookup — so
// the claim the string row can make is parity with the no-lookup churn rather
// than zero. That is what mapGetStrValueParityProg gates; arm64 and wasm
// cell-box the column and read 0 either way.

import "testing"

// Per-round bytes must be zero for every counted-value lookup shape. Each gate
// measures the 2n delta, so a fixed startup cost divides away and only a
// per-round leak survives. The exit code names the shape that leaked.
const mapGetReclaimBytesProg = `
import "core/map";

struct Q { a: i32, xs: i32[] }

function arr_stmt_churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[i32, i32[]] = map_new(4);
        m = m.insert(1, [i, i + 1]);
        match (m.get(1)) {
            Some(g) => { acc = (acc + g[0]) % 251; },
            None => { acc = (acc + 1) % 251; }
        }
        i = i + 1;
    }
    return acc;
}

function arr_expr_churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[i32, i32[]] = map_new(4);
        m = m.insert(1, [i, i + 1]);
        acc = (acc + match (m.get(1)) { Some(g) => g[0], None => 0 }) % 251;
        i = i + 1;
    }
    return acc;
}

function struct_churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[i32, Q] = map_new(4);
        m = m.insert(1, Q { a: i, xs: [i, i + 1] });
        match (m.get(1)) {
            Some(g) => { acc = (acc + g.a + g.xs[1]) % 251; },
            None => { acc = (acc + 1) % 251; }
        }
        i = i + 1;
    }
    return acc;
}

// The other value kinds mapGetHandsCountedValue admits: a generic-enum value,
// a tuple value and an array-of-struct value all reach the counted verdict
// through mapValHasDrop rather than through the array short-circuit, and a
// string KEY puts the lookup on the boxed-key path.
function opt_churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[i32, Option[i32]] = map_new(4);
        m = m.insert(1, Some(i));
        match (m.get(1)) {
            Some(g) => { match (g) { Some(v) => { acc = (acc + v) % 251; }, None => {} } },
            None => { acc = (acc + 1) % 251; }
        }
        i = i + 1;
    }
    return acc;
}

function tuple_churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[i32, (i32, i32)] = map_new(4);
        m = m.insert(1, (i, i + 1));
        match (m.get(1)) {
            Some(g) => { acc = (acc + g.0) % 251; },
            None => { acc = (acc + 1) % 251; }
        }
        i = i + 1;
    }
    return acc;
}

function strkey_churn(n: i32, s: string): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[string, i32[]] = map_new(4);
        m = m.insert(s, [i, i + 1]);
        match (m.get(s)) {
            Some(g) => { acc = (acc + g[1]) % 251; },
            None => { acc = (acc + 1) % 251; }
        }
        i = i + 1;
    }
    return acc;
}

function structarr_churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[i32, Q[]] = map_new(4);
        m = m.insert(1, [Q { a: i, xs: [i] }]);
        match (m.get(1)) {
            Some(g) => { acc = (acc + g[0].a) % 251; },
            None => { acc = (acc + 1) % 251; }
        }
        i = i + 1;
    }
    return acc;
}

// A MISS leaves the shared None sentinel in the scrutinee slot: the reclaim's
// is_unique gate must decline it rather than walk a payload that is not there.
function miss_churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[i32, i32[]] = map_new(4);
        m = m.insert(1, [i, i + 1]);
        match (m.get(99)) {
            Some(g) => { acc = (acc + g[0]) % 251; },
            None => { acc = (acc + 3) % 251; }
        }
        i = i + 1;
    }
    return acc;
}

function main(): i32 {
    var s: string = "a-key-past-the-inline-threshold-abcdefghi";

    if (arr_stmt_churn(1000) < 0) { return 11; }
    var a1: i64 = __heap_bump_bytes();
    if (arr_stmt_churn(2000) < 0) { return 11; }
    var a2: i64 = __heap_bump_bytes();
    if ((a2 - a1) / 2000 != 0) { return 1; }

    if (arr_expr_churn(1000) < 0) { return 12; }
    var b1: i64 = __heap_bump_bytes();
    if (arr_expr_churn(2000) < 0) { return 12; }
    var b2: i64 = __heap_bump_bytes();
    if ((b2 - b1) / 2000 != 0) { return 2; }

    if (struct_churn(1000) < 0) { return 13; }
    var c1: i64 = __heap_bump_bytes();
    if (struct_churn(2000) < 0) { return 13; }
    var c2: i64 = __heap_bump_bytes();
    if ((c2 - c1) / 2000 != 0) { return 3; }

    if (opt_churn(1000) < 0) { return 14; }
    var d1: i64 = __heap_bump_bytes();
    if (opt_churn(2000) < 0) { return 14; }
    var d2: i64 = __heap_bump_bytes();
    if ((d2 - d1) / 2000 != 0) { return 4; }

    if (tuple_churn(1000) < 0) { return 15; }
    var e1: i64 = __heap_bump_bytes();
    if (tuple_churn(2000) < 0) { return 15; }
    var e2: i64 = __heap_bump_bytes();
    if ((e2 - e1) / 2000 != 0) { return 5; }

    if (strkey_churn(1000, s) < 0) { return 16; }
    var f1: i64 = __heap_bump_bytes();
    if (strkey_churn(2000, s) < 0) { return 16; }
    var f2: i64 = __heap_bump_bytes();
    if ((f2 - f1) / 2000 != 0) { return 6; }

    if (structarr_churn(1000) < 0) { return 17; }
    var g1: i64 = __heap_bump_bytes();
    if (structarr_churn(2000) < 0) { return 17; }
    var g2: i64 = __heap_bump_bytes();
    if ((g2 - g1) / 2000 != 0) { return 7; }

    if (miss_churn(1000) < 0) { return 18; }
    var h1: i64 = __heap_bump_bytes();
    if (miss_churn(2000) < 0) { return 18; }
    var h2: i64 = __heap_bump_bytes();
    if ((h2 - h1) / 2000 != 0) { return 8; }

    if (s.len() != 41) { return 19; }
    if (__rc_underflow_count() != 0) { return 9; }
    return 42;
}
`

func TestMapGetReclaimBytesInterp(t *testing.T) {
	if got := runInterpExit(t, mapGetReclaimBytesProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapGetReclaimBytesX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapGetReclaimBytesProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (1..8 = that lookup shape strands its retain, 9 = over-release)", got)
	}
}

func TestMapGetReclaimBytesArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapGetReclaimBytesProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (1..8 = that lookup shape strands its retain, 9 = over-release)", got)
	}
}

func TestMapGetReclaimBytesWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapGetReclaimBytesProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (1..8 = that lookup shape strands its retain, 9 = over-release)", got)
	}
}

// A string VALUE column is the one shape with a backend residual of its own:
// x86-64 leaves kind-1 values unreclaimed by the map's drop, 64 B/round whether
// or not anything looks the value up. The claim the fix makes there is parity —
// the lookup costs no more per round than the same churn without it. Pre-fix the
// lookup form reads 64 | 64 | 64 against the no-lookup form's 64 | 0 | 0.
const mapGetStrValueParityProg = `
import "core/map";
import "std/string";

function with_get(n: i32, s: string): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[i32, string] = map_new(4);
        m = m.insert(1, s + "x");
        match (m.get(1)) {
            Some(g) => { acc = (acc + g.len()) % 251; },
            None => { acc = (acc + 1) % 251; }
        }
        i = i + 1;
    }
    return acc;
}

function no_get(n: i32, s: string): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var m: Map[i32, string] = map_new(4);
        m = m.insert(1, s + "x");
        acc = (acc + m.len()) % 251;
        i = i + 1;
    }
    return acc;
}

function main(): i32 {
    var s: string = "a-string-past-the-inline-threshold-abcdefgh";

    if (no_get(1000, s) < 0) { return 11; }
    var a1: i64 = __heap_bump_bytes();
    if (no_get(2000, s) < 0) { return 11; }
    var a2: i64 = __heap_bump_bytes();
    var plainPer: i64 = (a2 - a1) / 2000;

    if (with_get(1000, s) < 0) { return 12; }
    var b1: i64 = __heap_bump_bytes();
    if (with_get(2000, s) < 0) { return 12; }
    var b2: i64 = __heap_bump_bytes();
    var getPer: i64 = (b2 - b1) / 2000;

    if (getPer > plainPer) { return 1; }
    if (__rc_underflow_count() != 0) { return 2; }
    return 42;
}
`

func TestMapGetStrValueParityX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapGetStrValueParityProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (1 = the lookup costs more per round than the same churn without it)", got)
	}
}

func TestMapGetStrValueParityArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapGetStrValueParityProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (1 = the lookup costs more per round than the same churn without it)", got)
	}
}

func TestMapGetStrValueParityWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapGetStrValueParityProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (1 = the lookup costs more per round than the same churn without it)", got)
	}
}

// The aliasing side: what the reclaim must NOT free. The whole risk of paying
// back the lookup's retain is over-release, so every case here reads its value
// AFTER the map that owned it has died, with a burst of churn in between so a
// block that had been freed would already have been handed out again.
//
// The escaping-binding rows are the ones the fix is judged on: `keep = g` moves
// the binding out of the arm, so the reclaim runs while a live name still points
// at the value. It stays readable because the reclaim releases the LOOKUP's
// count, not the map's — the shape is one count over, never one short.
const mapGetAliasProg = `
import "core/map";
import "std/array";

struct Q { a: i32, xs: i32[] }

function churn(k: i32): i32 {
    var s: i32 = 0;
    var j: i32 = 0;
    while (j < k) {
        var t: i32[] = [j, j + 1, j + 2, j + 3];
        s = (s + t[3]) % 251;
        j = j + 1;
    }
    return s;
}

// The binding escapes into an outer local and is read on the NEXT round, after
// its map has been dropped.
function escape_outer(n: i32): i32 {
    var keep: i32[] = [0, 0];
    var bad: i32 = 0;
    var i: i32 = 1;
    while (i < n) {
        if (i > 1) {
            if (keep[0] != i - 1) { bad = bad + 1; }
            if (keep[1] != i) { bad = bad + 1; }
        }
        var m: Map[i32, i32[]] = map_new(4);
        m = m.insert(1, [i, i + 1]);
        match (m.get(1)) {
            Some(g) => { keep = g; },
            None => {}
        }
        if (churn(8) < 0) { return 0 - 1; }
        i = i + 1;
    }
    if (keep[0] != n - 1) { bad = bad + 1; }
    return bad;
}

// The sharpest one: the binding escapes AND the owning map is destroyed inside
// the same arm, so the reclaim runs with the lookup's count as the only one left.
function escape_map_dies(n: i32): i32 {
    var keep: i32[] = [0, 0];
    var bad: i32 = 0;
    var i: i32 = 1;
    while (i < n) {
        if (i > 1) {
            if (keep[0] != i - 1) { bad = bad + 1; }
            if (keep[1] != i) { bad = bad + 1; }
        }
        var m: Map[i32, i32[]] = map_new(4);
        m = m.insert(1, [i, i + 1]);
        match (m.get(1)) {
            Some(g) => { keep = g; m = map_new(4); },
            None => {}
        }
        if (churn(8) < 0) { return 0 - 1; }
        if (m.len() != 0) { return 0 - 2; }
        i = i + 1;
    }
    if (keep[0] != n - 1) { bad = bad + 1; }
    return bad;
}

// The arm RETURNS the binding out of the function.
function pick(m: Map[i32, i32[]], k: i32): i32[] {
    match (m.get(k)) {
        Some(g) => { return g; },
        None => {}
    }
    return [0, 0];
}
function escape_return(n: i32): i32 {
    var bad: i32 = 0;
    var i: i32 = 1;
    while (i < n) {
        var m: Map[i32, i32[]] = map_new(4);
        m = m.insert(1, [i, i + 1]);
        var got: i32[] = pick(m, 1);
        if (churn(8) < 0) { return 0 - 1; }
        if (got[0] != i) { bad = bad + 1; }
        if (got[1] != i + 1) { bad = bad + 1; }
        i = i + 1;
    }
    return bad;
}

// The binding is stored into a struct that outlives the arm.
function escape_into_struct(n: i32): i32 {
    var bad: i32 = 0;
    var i: i32 = 1;
    while (i < n) {
        var m: Map[i32, i32[]] = map_new(4);
        m = m.insert(1, [i, i + 1]);
        var q: Q = Q { a: 0, xs: [0, 0] };
        match (m.get(1)) {
            Some(g) => { q = Q { a: i, xs: g }; },
            None => {}
        }
        if (churn(8) < 0) { return 0 - 1; }
        if (q.xs[1] != i + 1) { bad = bad + 1; }
        i = i + 1;
    }
    return bad;
}

// The binding is put BACK into the map it came from, then read through it.
function escape_reinsert(n: i32): i32 {
    var bad: i32 = 0;
    var i: i32 = 1;
    while (i < n) {
        var m: Map[i32, i32[]] = map_new(4);
        m = m.insert(1, [i, i + 1]);
        match (m.get(1)) {
            Some(g) => { m = m.insert(2, g.append(7)); },
            None => {}
        }
        if (churn(8) < 0) { return 0 - 1; }
        if (m.get_or(2, [0])[2] != 7) { bad = bad + 1; }
        if (m.get_or(1, [0])[0] != i) { bad = bad + 1; }
        i = i + 1;
    }
    return bad;
}

// A live local array is ALSO the map's value: the reclaim must leave the local
// readable.
function alias_local(n: i32): i32 {
    var bad: i32 = 0;
    var i: i32 = 1;
    while (i < n) {
        var live: i32[] = [i, i + 1];
        var m: Map[i32, i32[]] = map_new(4);
        m = m.insert(1, live);
        match (m.get(1)) {
            Some(g) => { if (g[0] != i) { bad = bad + 1; } },
            None => { bad = bad + 1; }
        }
        if (churn(8) < 0) { return 0 - 1; }
        if (live[1] != i + 1) { bad = bad + 1; }
        i = i + 1;
    }
    return bad;
}

function main(): i32 {
    if (escape_outer(200) != 0) { return 1; }
    if (escape_map_dies(200) != 0) { return 2; }
    if (escape_return(200) != 0) { return 3; }
    if (escape_into_struct(200) != 0) { return 4; }
    if (escape_reinsert(200) != 0) { return 5; }
    if (alias_local(200) != 0) { return 6; }
    if (__rc_underflow_count() != 0) { return 7; }
    return 42;
}
`

func TestMapGetAliasInterp(t *testing.T) {
	if got := runInterpExit(t, mapGetAliasProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapGetAliasX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapGetAliasProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (1..6 = that alias read wrong, 7 = over-release)", got)
	}
}

func TestMapGetAliasArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapGetAliasProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (1..6 = that alias read wrong, 7 = over-release)", got)
	}
}

func TestMapGetAliasWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapGetAliasProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (1..6 = that alias read wrong, 7 = over-release)", got)
	}
}
