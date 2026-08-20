package e2e

// An arm binding assigned to an OUTER local is an owner, and reclaims (#7163).
//
// A match / if-let binding is borrow-tainted by computeFreeEligible: it aliases
// an enum payload with no projection dup, so the analysis must not let it free.
// That taint used to PROPAGATE through `keep = g` to the destination local. But
// that assignment is not the alias — needsRcIncOnAlias fires on it, so `keep`
// takes a reference of its own. Inheriting the taint cost `keep` its
// free-eligibility, and its overwrite-dec fell back to the catch-all flat
// __fern_rc_dec, which decrements without reclaiming: one stranded buffer per
// assignment, unbounded in a loop.
//
// Under the sanitizer, on the correctness program below:
//
//	before   allocs=4003 frees=2503 live_bytes=48000  (leak 48000 in 1500 blocks)
//	after    allocs=4003 frees=4003 live_bytes=0
//
// The defect is in the ASSIGNMENT, not in `get`: a binding taken from a struct
// field's Option, from an array element's Option, or by `if let` leaks exactly
// the same way, and all are gated here. #7163 reported it only through
// `m.get(k)` because that is where it was found.
//
// The issue's stated cause — "neither an alias inc nor an overwrite dec, it
// lowers as a move" — does not match the tree it was filed against any more.
// The alias inc does fire. What is missing is the RECLAIMING dec, and the
// reason is the taint above, which is why the fix is in the eligibility
// analysis rather than at the move-vs-copy decision.
//
// The controls are load-bearing in the other direction. get_or_churn does the
// same work with no arm binding, and nonescape_churn keeps the binding local
// (the #7144 shape); both were already flat, so a fix that simply freed more
// aggressively would not be distinguishable from one that is correct. The
// read-after-death, two-escapee and re-alias legs plus __rc_underflow_count
// are what say the reclaim is not an over-release — the direction this area
// fails in (#7122 exit 134, #7114 wrong answer, #7145 a walk that never ran).
//
// Reverting the fix fails x86-64, arm64 and wasm on code 1. The INTERP leg
// still passes: the interpreter runs no RC lowering, so the byte gates are
// trivially flat there. It is kept for what it does gate — that the expected
// values in the correctness legs are the language's answer rather than
// whatever the compiler happens to produce — and not counted as coverage of
// the reclaim itself.

import "testing"

const matchBindingEscapeProg = `
import "core/map";

struct Holder { o: Option[i32[]] }

// --- the reported shape: arm binding from a map lookup, escaping ---
function map_escape_churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    var keep: i32[] = [0, 0];
    while (i < n) {
        var m: Map[i32, i32[]] = map_new(4);
        m = m.insert(1, [i, i + 1]);
        match (m.get(1)) {
            Some(g) => { keep = g; },
            None => {}
        }
        acc = (acc + keep[0]) % 251;
        i = i + 1;
    }
    return acc;
}

// --- control: same work, no arm binding (already flat before the fix) ---
function get_or_churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    var fb: i32[] = [0, 0];
    var keep: i32[] = [0, 0];
    while (i < n) {
        var m: Map[i32, i32[]] = map_new(4);
        m = m.insert(1, [i, i + 1]);
        keep = m.get_or(1, fb);
        acc = (acc + keep[0]) % 251;
        i = i + 1;
    }
    return acc;
}

// --- control: arm binding that does NOT escape (the #7144 shape) ---
function nonescape_churn(n: i32): i32 {
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

// --- the same defect with NO map involved ---
function field_escape_churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    var keep: i32[] = [0, 0];
    while (i < n) {
        var h: Holder = Holder { o: Some([i, i + 5]) };
        match (h.o) { Some(g) => { keep = g; }, None => {} }
        acc = (acc + keep[1]) % 251;
        i = i + 1;
    }
    return acc;
}

function iflet_escape_churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    var keep: i32[] = [0, 0];
    while (i < n) {
        var o: Option[i32[]] = Some([i, i + 11]);
        if let Some(g) = o { keep = g; }
        acc = (acc + keep[1]) % 251;
        i = i + 1;
    }
    return acc;
}

// --- correctness: the escapee outlives the map that owned it ---
function read_after_map_death(n: i32): i32 {
    var keep: i32[] = [0, 0];
    var i: i32 = 0;
    while (i < n) {
        var m: Map[i32, i32[]] = map_new(4);
        m = m.insert(1, [i, i + 7]);
        match (m.get(1)) { Some(g) => { keep = g; }, None => {} }
        i = i + 1;
    }
    return keep[0] * 1000 + keep[1];
}

// --- correctness: two live escapees from two maps, interleaved ---
function two_escapees(n: i32): i32 {
    var a: i32[] = [0, 0];
    var b: i32[] = [0, 0];
    var i: i32 = 0;
    while (i < n) {
        var m1: Map[i32, i32[]] = map_new(4);
        m1 = m1.insert(1, [i, i + 1]);
        var m2: Map[i32, i32[]] = map_new(4);
        m2 = m2.insert(2, [i + 100, i + 101]);
        match (m1.get(1)) { Some(g) => { a = g; }, None => {} }
        match (m2.get(2)) { Some(g) => { b = g; }, None => {} }
        if (a[0] != i) { return 0 - 1; }
        if (b[0] != i + 100) { return 0 - 2; }
        i = i + 1;
    }
    return a[1] + b[1];
}

// --- correctness: the escapee aliased again afterwards; both stay live ---
function alias_after_escape(n: i32): i32 {
    var keep: i32[] = [0, 0];
    var alias: i32[] = [0, 0];
    var i: i32 = 0;
    while (i < n) {
        var m: Map[i32, i32[]] = map_new(4);
        m = m.insert(1, [i, i + 3]);
        match (m.get(1)) { Some(g) => { keep = g; }, None => {} }
        alias = keep;
        if (alias[1] != i + 3) { return 0 - 3; }
        i = i + 1;
    }
    return alias[1];
}

function main(): i32 {
    if (map_escape_churn(1000) < 0) { return 11; }
    var a1: i64 = __heap_bump_bytes();
    if (map_escape_churn(2000) < 0) { return 11; }
    var a2: i64 = __heap_bump_bytes();
    if ((a2 - a1) / 2000 != 0) { return 1; }

    if (get_or_churn(1000) < 0) { return 12; }
    var b1: i64 = __heap_bump_bytes();
    if (get_or_churn(2000) < 0) { return 12; }
    var b2: i64 = __heap_bump_bytes();
    if ((b2 - b1) / 2000 != 0) { return 2; }

    if (nonescape_churn(1000) < 0) { return 13; }
    var c1: i64 = __heap_bump_bytes();
    if (nonescape_churn(2000) < 0) { return 13; }
    var c2: i64 = __heap_bump_bytes();
    if ((c2 - c1) / 2000 != 0) { return 3; }

    if (field_escape_churn(1000) < 0) { return 14; }
    var d1: i64 = __heap_bump_bytes();
    if (field_escape_churn(2000) < 0) { return 14; }
    var d2: i64 = __heap_bump_bytes();
    if ((d2 - d1) / 2000 != 0) { return 4; }

    if (iflet_escape_churn(1000) < 0) { return 15; }
    var e1: i64 = __heap_bump_bytes();
    if (iflet_escape_churn(2000) < 0) { return 15; }
    var e2: i64 = __heap_bump_bytes();
    if ((e2 - e1) / 2000 != 0) { return 5; }

    if (read_after_map_death(500) != 499 * 1000 + 506) { return 6; }
    if (two_escapees(500) != 500 + 600) { return 7; }
    if (alias_after_escape(500) != 502) { return 8; }

    if (__rc_underflow_count() != 0) { return 9; }
    return 42;
}
`

// codes: 1..5 = that shape strands its buffer per round; 6..8 = the reclaim is an
// over-release (wrong value read back, or a control leg tripped); 9 = rc
// underflow; 11..15 = a churn returned negative, i.e. its own invariant failed.
const matchBindingEscapeWant = "want 42 (1-5 = that shape leaks per round, " +
	"6-8 = over-release read back a wrong value, 9 = rc underflow, 11-15 = churn self-check failed)"

func TestMatchBindingEscapeReclaimInterp(t *testing.T) {
	if got := runInterpExit(t, matchBindingEscapeProg); got != 42 {
		t.Fatalf("interp got %d, %s", got, matchBindingEscapeWant)
	}
}

func TestMatchBindingEscapeReclaimX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, matchBindingEscapeProg); got != 42 {
		t.Fatalf("x86-64 got %d, %s", got, matchBindingEscapeWant)
	}
}

func TestMatchBindingEscapeReclaimArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, matchBindingEscapeProg); got != 42 {
		t.Fatalf("arm64 got %d, %s", got, matchBindingEscapeWant)
	}
}

func TestMatchBindingEscapeReclaimWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, matchBindingEscapeProg); got != 42 {
		t.Fatalf("wasm got %d, %s", got, matchBindingEscapeWant)
	}
}
