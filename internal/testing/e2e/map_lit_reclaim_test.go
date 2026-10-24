package e2e

import "testing"

const mapLitReclaimBytesProg = `
import "core/map";

struct Q { a: i32, xs: i32[] }

function scalar_churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[i32, i32] = Map {};
        m = m.insert(i, i + 1);
        m = m.insert(i + 1, i + 2);
        acc = (acc + m.len()) % 251;
        i = i + 1;
    }
    return acc;
}

function strkey_churn(n: i32, s: string): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[string, i32] = Map { s: i };
        acc = (acc + m.len()) % 251;
        i = i + 1;
    }
    return acc;
}

function strval_churn(n: i32, s: string): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[i32, string] = Map { 1: s };
        acc = (acc + m.len()) % 251;
        i = i + 1;
    }
    return acc;
}

function structval_churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[i32, Q] = Map { 1: Q { a: i, xs: [i, i + 1] } };
        acc = (acc + m.len()) % 251;
        i = i + 1;
    }
    return acc;
}

function main(): i32 {
    let s: string = "a-map-key-past-the-inline-threshold-abcdef";

    if (scalar_churn(1000) < 0) { return 11; }
    let a1: i64 = __heap_bump_bytes();
    if (scalar_churn(2000) < 0) { return 11; }
    let a2: i64 = __heap_bump_bytes();
    if ((a2 - a1) / 2000 != 0) { return 1; }

    if (strkey_churn(1000, s) < 0) { return 12; }
    let b1: i64 = __heap_bump_bytes();
    if (strkey_churn(2000, s) < 0) { return 12; }
    let b2: i64 = __heap_bump_bytes();
    if ((b2 - b1) / 2000 != 0) { return 2; }

    if (strval_churn(1000, s) < 0) { return 13; }
    let c1: i64 = __heap_bump_bytes();
    if (strval_churn(2000, s) < 0) { return 13; }
    let c2: i64 = __heap_bump_bytes();
    if ((c2 - c1) / 2000 != 0) { return 3; }

    if (structval_churn(1000) < 0) { return 14; }
    let d1: i64 = __heap_bump_bytes();
    if (structval_churn(2000) < 0) { return 14; }
    let d2: i64 = __heap_bump_bytes();
    if ((d2 - d1) / 2000 != 0) { return 4; }

    if (s.len() != 42) { return 15; }
    if (__rc_underflow_count() != 0) { return 16; }
    return 42;
}
`

func TestMapLitReclaimBytesInterp(t *testing.T) {
	if got := runInterpExit(t, mapLitReclaimBytesProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapLitReclaimBytesX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapLitReclaimBytesProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (1..4 = that shape's map literal is not reclaimed)", got)
	}
}

func TestMapLitReclaimBytesArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapLitReclaimBytesProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (1..4 = that shape's map literal is not reclaimed)", got)
	}
}

func TestMapLitReclaimBytesWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapLitReclaimBytesProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (1..4 = that shape's map literal is not reclaimed)", got)
	}
}

const mapLitArrValueParityProg = `
import "core/map";

function lit_churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[i32, i32[]] = Map { 1: [i, i + 1] };
        acc = (acc + m.len()) % 251;
        i = i + 1;
    }
    return acc;
}

function insert_churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[i32, i32[]] = map_new(1);
        m = m.insert(1, [i, i + 1]);
        acc = (acc + m.len()) % 251;
        i = i + 1;
    }
    return acc;
}

function main(): i32 {
    if (insert_churn(1000) < 0) { return 11; }
    let a1: i64 = __heap_bump_bytes();
    if (insert_churn(2000) < 0) { return 11; }
    let a2: i64 = __heap_bump_bytes();
    let insertPer: i64 = (a2 - a1) / 2000;

    if (lit_churn(1000) < 0) { return 12; }
    let b1: i64 = __heap_bump_bytes();
    if (lit_churn(2000) < 0) { return 12; }
    let b2: i64 = __heap_bump_bytes();
    let litPer: i64 = (b2 - b1) / 2000;

    if (litPer > insertPer) { return 1; }
    if (__rc_underflow_count() != 0) { return 2; }
    return 42;
}
`

func TestMapLitArrValueParityX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapLitArrValueParityProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (1 = the literal costs more per round than the insert form)", got)
	}
}

func TestMapLitArrValueParityArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapLitArrValueParityProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (1 = the literal costs more per round than the insert form)", got)
	}
}

func TestMapLitArrValueParityWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapLitArrValueParityProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (1 = the literal costs more per round than the insert form)", got)
	}
}

const mapLitAliasProg = `
import "core/map";

struct Q { a: i32, xs: i32[] }
struct Holder { m: Map[i32, i32] }

// A live local array stored as a literal's VALUE: the map's column drop must
// not pull the buffer out from under the local that still names it.
function alias_value(n: i32): i32 {
    let live: i32[] = [7, 8, 9];
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[i32, i32[]] = Map { 1: live };
        acc = (acc + m.len()) % 251;
        i = i + 1;
    }
    return (acc + live[0] + live[1] + live[2]) % 251;
}

// The literal ESCAPES the frame that built it.
function mk_map(k: i32): Map[i32, i32] {
    let m: Map[i32, i32] = Map { 1: k, 2: k + 1 };
    return m;
}
function escape_return(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[i32, i32] = mk_map(i);
        acc = (acc + m.get_or(2, 0)) % 251;
        i = i + 1;
    }
    return acc;
}

// The literal is stored into a struct field and read through it.
function into_struct(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[i32, i32] = Map { 1: i, 2: i + 1 };
        let h: Holder = Holder { m: m };
        acc = (acc + h.m.get_or(1, 0)) % 251;
        i = i + 1;
    }
    return acc;
}

// A second binding names the same handle; both are read.
function alias_local(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[i32, i32] = Map { 1: i };
        let m2: Map[i32, i32] = m;
        acc = (acc + m.get_or(1, 0) + m2.get_or(1, 0)) % 251;
        i = i + 1;
    }
    return acc;
}

// A VALUE read out of the literal outlives the round it came from — the
// inc-on-get co-ownership the column drop must respect.
function value_out(n: i32): i32 {
    let keep: i32[] = [0];
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[i32, i32[]] = Map { 1: [i, i + 1] };
        match (m.get(1)) { Some(g) => { keep = g; }, None => { acc = acc + 1; } }
        acc = (acc + keep[0]) % 251;
        i = i + 1;
    }
    return (acc + keep.len()) % 251;
}

// String key and string value both sourced from a live local.
function string_cols(n: i32, s: string): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[string, string] = Map { s: s };
        acc = (acc + m.len() + m.get_or(s, "").len()) % 251;
        i = i + 1;
    }
    return (acc + s.len()) % 251;
}

// Struct values, read back out of the literal.
function struct_values(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[i32, Q] = Map { 1: Q { a: i, xs: [i, i + 1] } };
        match (m.get(1)) { Some(q) => { acc = (acc + q.a + q.xs[1]) % 251; }, None => { acc = acc + 1; } }
        i = i + 1;
    }
    return acc;
}

function main(): i32 {
    let s: string = "a-string-past-the-inline-threshold-abcdef";
    if (alias_value(200) != 224) { return 1; }
    if (escape_return(200) != 20) { return 2; }
    if (into_struct(200) != 71) { return 3; }
    if (alias_local(200) != 142) { return 4; }
    if (value_out(200) != 73) { return 5; }
    if (string_cols(200, s) != 158) { return 6; }
    if (struct_values(200) != 91) { return 7; }
    if (__rc_underflow_count() != 0) { return 8; }
    return 42;
}
`

func TestMapLitAliasInterp(t *testing.T) {
	if got := runInterpExit(t, mapLitAliasProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapLitAliasX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapLitAliasProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (1..7 name the aliasing case that read wrong)", got)
	}
}

func TestMapLitAliasArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapLitAliasProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (1..7 name the aliasing case that read wrong)", got)
	}
}

func TestMapLitAliasWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapLitAliasProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (1..7 name the aliasing case that read wrong)", got)
	}
}
