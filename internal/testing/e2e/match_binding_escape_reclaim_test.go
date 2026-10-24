package e2e

import "testing"

const matchBindingEscapeProg = `
import "core/map";

struct Holder { o: Option[i32[]] }

// --- the reported shape: arm binding from a map lookup, escaping ---
function map_escape_churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    let keep: i32[] = [0, 0];
    while (i < n) {
        let m: Map[i32, i32[]] = map_new(4);
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
    let acc: i32 = 0;
    let i: i32 = 0;
    let fb: i32[] = [0, 0];
    let keep: i32[] = [0, 0];
    while (i < n) {
        let m: Map[i32, i32[]] = map_new(4);
        m = m.insert(1, [i, i + 1]);
        keep = m.get_or(1, fb);
        acc = (acc + keep[0]) % 251;
        i = i + 1;
    }
    return acc;
}

// --- control: arm binding that does NOT escape (the #7144 shape) ---
function nonescape_churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[i32, i32[]] = map_new(4);
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
    let acc: i32 = 0;
    let i: i32 = 0;
    let keep: i32[] = [0, 0];
    while (i < n) {
        let h: Holder = Holder { o: Some([i, i + 5]) };
        match (h.o) { Some(g) => { keep = g; }, None => {} }
        acc = (acc + keep[1]) % 251;
        i = i + 1;
    }
    return acc;
}

function iflet_escape_churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    let keep: i32[] = [0, 0];
    while (i < n) {
        let o: Option[i32[]] = Some([i, i + 11]);
        if let Some(g) = o { keep = g; }
        acc = (acc + keep[1]) % 251;
        i = i + 1;
    }
    return acc;
}

// --- correctness: the escapee outlives the map that owned it ---
function read_after_map_death(n: i32): i32 {
    let keep: i32[] = [0, 0];
    let i: i32 = 0;
    while (i < n) {
        let m: Map[i32, i32[]] = map_new(4);
        m = m.insert(1, [i, i + 7]);
        match (m.get(1)) { Some(g) => { keep = g; }, None => {} }
        i = i + 1;
    }
    return keep[0] * 1000 + keep[1];
}

// --- correctness: two live escapees from two maps, interleaved ---
function two_escapees(n: i32): i32 {
    let a: i32[] = [0, 0];
    let b: i32[] = [0, 0];
    let i: i32 = 0;
    while (i < n) {
        let m1: Map[i32, i32[]] = map_new(4);
        m1 = m1.insert(1, [i, i + 1]);
        let m2: Map[i32, i32[]] = map_new(4);
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
    let keep: i32[] = [0, 0];
    let alias: i32[] = [0, 0];
    let i: i32 = 0;
    while (i < n) {
        let m: Map[i32, i32[]] = map_new(4);
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
    let a1: i64 = __heap_bump_bytes();
    if (map_escape_churn(2000) < 0) { return 11; }
    let a2: i64 = __heap_bump_bytes();
    if ((a2 - a1) / 2000 != 0) { return 1; }

    if (get_or_churn(1000) < 0) { return 12; }
    let b1: i64 = __heap_bump_bytes();
    if (get_or_churn(2000) < 0) { return 12; }
    let b2: i64 = __heap_bump_bytes();
    if ((b2 - b1) / 2000 != 0) { return 2; }

    if (nonescape_churn(1000) < 0) { return 13; }
    let c1: i64 = __heap_bump_bytes();
    if (nonescape_churn(2000) < 0) { return 13; }
    let c2: i64 = __heap_bump_bytes();
    if ((c2 - c1) / 2000 != 0) { return 3; }

    if (field_escape_churn(1000) < 0) { return 14; }
    let d1: i64 = __heap_bump_bytes();
    if (field_escape_churn(2000) < 0) { return 14; }
    let d2: i64 = __heap_bump_bytes();
    if ((d2 - d1) / 2000 != 0) { return 4; }

    if (iflet_escape_churn(1000) < 0) { return 15; }
    let e1: i64 = __heap_bump_bytes();
    if (iflet_escape_churn(2000) < 0) { return 15; }
    let e2: i64 = __heap_bump_bytes();
    if ((e2 - e1) / 2000 != 0) { return 5; }

    if (read_after_map_death(500) != 499 * 1000 + 506) { return 6; }
    if (two_escapees(500) != 500 + 600) { return 7; }
    if (alias_after_escape(500) != 502) { return 8; }

    if (__rc_underflow_count() != 0) { return 9; }
    return 42;
}
`

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
