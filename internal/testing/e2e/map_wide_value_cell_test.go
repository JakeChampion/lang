package e2e

import "testing"

const mapWideValueOverwriteHeapProg = `
import "core/map";
import "std/i32";

function fillWide(n: i32): i64 {
    let m: Map[i32, i64] = map_new(64);
    let i: i32 = 0;
    while (i < n) {
        m = m.insert(i % 32, i as i64);
        i = i + 1;
    }
    if (m.len() != 32) { return 0 - 1; }
    return __heap_bump_bytes();
}

function fillFloat(n: i32): i64 {
    let m: Map[i32, f64] = map_new(64);
    let i: i32 = 0;
    while (i < n) {
        m = m.insert(i % 32, 1.5);
        i = i + 1;
    }
    if (m.len() != 32) { return 0 - 1; }
    return __heap_bump_bytes();
}

function churnDelete(n: i32): i64 {
    let m: Map[i32, i64] = map_new(64);
    let i: i32 = 0;
    while (i < n) {
        m = m.insert(7, i as i64);
        let ok: boolean = m.contains(7);
        let m2 = m.remove(7);
        if (!ok) { return 0 - 1; }
        m = m2;
        i = i + 1;
    }
    if (m.len() != 0) { return 0 - 2; }
    return __heap_bump_bytes();
}

function churnClear(n: i32): i64 {
    let m: Map[i32, i64] = map_new(64);
    let i: i32 = 0;
    while (i < n) {
        m = m.insert(1, i as i64);
        m = m.insert(2, i as i64);
        let c = m.cleared();
        m = c;
        i = i + 1;
    }
    if (m.len() != 0) { return 0 - 1; }
    return __heap_bump_bytes();
}

function main(): i32 {
    let w1: i64 = fillWide(100);
    let w2: i64 = fillWide(1600);
    if (w1 < 0) { return 1; }
    if (w2 > w1) { return 2; }
    let f1: i64 = fillFloat(100);
    let f2: i64 = fillFloat(1600);
    if (f1 < 0) { return 3; }
    if (f2 > f1) { return 4; }
    let d1: i64 = churnDelete(100);
    let d2: i64 = churnDelete(1600);
    if (d1 < 0) { return 5; }
    if (d2 > d1) { return 6; }
    let c1: i64 = churnClear(100);
    let c2: i64 = churnClear(1600);
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

const mapWideValueOverwriteAliasProg = `
import "core/map";
import "std/i32";

struct Holder { m: Map[i32, i64] }

function main(): i32 {
    let m: Map[i32, i64] = map_new(8);
    m = m.insert(1, 11 as i64);
    m = m.insert(2, 22 as i64);
    let m2 = m.insert(3, 33 as i64);
    let h = Holder { m: m.insert(4, 44 as i64) };
    let i: i32 = 0;
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

const mapWideValueReadAfterOverwriteProg = `
import "core/map";
import "std/i32";

function main(): i32 {
    let w: Map[i64, i64] = map_new(8);
    let i: i32 = 0;
    while (i < 64) {
        w = w.insert((i % 4) as i64, (i * 10) as i64);
        i = i + 1;
    }
    if (w.len() != 4) { return 1; }
    if (w.get_or(3 as i64, 0 as i64) != 630 as i64) { return 2; }

    let m: Map[i32, i64] = map_new(8);
    m = m.insert(1, 100 as i64);
    m = m.insert(2, 200 as i64);
    m = m.insert(1, 111 as i64);
    let vs: i64[] = m.values();
    if (vs.len() != 2) { return 3; }
    let sum: i64 = 0 as i64;
    let j: i32 = 0;
    while (j < vs.len()) {
        sum = sum + vs[j];
        j = j + 1;
    }
    if (sum != 311 as i64) { return 4; }

    let isum: i64 = 0 as i64;
    for (k, v) in m {
        isum = isum + v;
    }
    if (isum != 311 as i64) { return 5; }

    // The Option outlives the cell it was read from.
    let got = m.get(1);
    m = m.insert(1, 999 as i64);
    let gv: i64 = match (got) { Some(x) => x, None => 0 as i64 };
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

const mapWideKeyCopyDeleteProg = `
import "core/map";

function main(): i32 {
    let m: Map[i64, i32] = map_new(8);
    let i: i32 = 0;
    while (i < 8) {
        m = m.insert((i as i64) * 1000000007, i);
        i = i + 1;
    }
    let m2 = m;
    let gone: boolean = m2.contains(3000000021);
    let m3 = m2.remove(3000000021);
    if (!gone || m3.len() != 7) { return 1; }
    let junk: i64[] = [];
    i = 0;
    while (i < 64) {
        junk = junk.append(77 as i64);
        i = i + 1;
    }
    let sum: i64 = 0;
    for k in m.keys() { sum = sum + k; }
    if (sum != 28000000196) { return 2; }
    if (m.get_or(3000000021, 0 - 1) != 3) { return 3; }
    return 42 + __rc_underflow_count();
}
`

func TestMapWideKeyCopyDeleteX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapWideKeyCopyDeleteProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (2 = the original read a key cell the copy freed)", got)
	}
}

func TestMapWideKeyCopyDeleteArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapWideKeyCopyDeleteProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (2 = the original read a key cell the copy freed)", got)
	}
}

func TestMapWideKeyCopyDeleteWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapWideKeyCopyDeleteProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (2 = the original read a key cell the copy freed)", got)
	}
}
