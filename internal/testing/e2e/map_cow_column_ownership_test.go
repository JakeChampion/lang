package e2e

import "testing"

const mapCowColumnOwnershipProg = `
import "core/map";
import "std/i32";

function main(): i32 {
    let a: Map[string, i32] = map_new(8);
    a = a.insert("a-fairly-long-key-that-heap-allocates", 7);
    let i: i32 = 0;
    while (i < 8) {
        let b = a;
        b = b.insert("x" + i.to_string(), i);
        // The copy sees the key it inherited...
        if (b.get_or("a-fairly-long-key-that-heap-allocates", 0 - 1) != 7) { return 1; }
        if (b.len() != 2) { return 2; }
        i = i + 1;
        // ...and is reinit-dropped here, on the next round's rebind of b.
    }
    // ...and the source still sees its own after every one of them died.
    if (a.get_or("a-fairly-long-key-that-heap-allocates", 0 - 1) != 7) { return 3; }
    if (a.len() != 1) { return 4; }

    // The same, one level up: an ARRAY value column (valKind 2) is counted
    // rather than boxed, so the claim is an inc per value instead of a rebox.
    let v: Map[string, i32[]] = map_new(8);
    v = v.insert("vk-a-fairly-long-key", [11, 22, 33]);
    let j: i32 = 0;
    while (j < 8) {
        let w = v;
        w = w.insert("y" + j.to_string(), [j, j]);
        if (w.get_or("vk-a-fairly-long-key", [0])[2] != 33) { return 5; }
        j = j + 1;
    }
    if (v.get_or("vk-a-fairly-long-key", [0])[2] != 33) { return 6; }

    // NOT covered, and deliberately absent: Map[string, string]. That column
    // is released by __drop_map_str_values on the same terms, but valKind 1
    // lumps a string value in with every other unreclaimed pointer, so the
    // copy side has no runtime test for it — see __map_own_copied_cols. The
    // key claim alone takes arm64 from SIGSEGV to a wrong value there, which
    // is an improvement and not a fix; the shape stays out of this suite until
    // the value half lands rather than pinning behaviour known to be wrong.

    // The copy is independent, not just intact: mutating it must not be
    // visible through the source.
    let s: Map[string, i32] = map_new(8);
    s = s.insert("shared-key-that-heap-allocates", 1);
    let c = s;
    c = c.insert("shared-key-that-heap-allocates", 2);
    if (s.get_or("shared-key-that-heap-allocates", 0 - 1) != 1) { return 7; }
    if (c.get_or("shared-key-that-heap-allocates", 0 - 1) != 2) { return 8; }

    return 42;
}
`

func TestMapCowColumnOwnershipInterp(t *testing.T) {
	if got := runInterpExit(t, mapCowColumnOwnershipProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapCowColumnOwnershipX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapCowColumnOwnershipProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestMapCowColumnOwnershipWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapCowColumnOwnershipProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestMapCowColumnOwnershipArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapCowColumnOwnershipProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

const mapCowColumnOwnershipRcProg = `
import "core/map";
import "std/i32";

function main(): i32 {
    let a: Map[string, i32] = map_new(8);
    a = a.insert("a-fairly-long-key-that-heap-allocates", 7);
    let i: i32 = 0;
    while (i < 16) {
        let b = a;
        b = b.insert("x" + i.to_string(), i);
        i = i + 1;
    }
    return 42 + __rc_underflow_count();
}
`

func TestMapCowColumnOwnershipNoUnderflowX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapCowColumnOwnershipRcProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (%d rc underflows)", got, got-42)
	}
}

func TestMapCowColumnOwnershipNoUnderflowWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapCowColumnOwnershipRcProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (%d rc underflows)", got, got-42)
	}
}

func TestMapCowColumnOwnershipNoUnderflowArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapCowColumnOwnershipRcProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (%d rc underflows)", got, got-42)
	}
}

const mapCowColumnOwnershipBoundedProg = `
import "core/map";
import "std/i32";

function churn(rounds: i32): i32 {
    let a: Map[string, i32] = map_new(16);
    let s: i32 = 0;
    while (s < 6) { a = a.insert("seed-key-that-heap-allocates" + s.to_string(), s); s = s + 1; }
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < rounds) {
        let b = a;
        b = b.insert("x" + i.to_string(), i);
        acc = (acc + b.len()) % 251;
        i = i + 1;
    }
    return acc;
}

function main(): i32 {
    if (churn(50) < 0) { return 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    if (churn(400) < 0) { return 2; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (churn(800) < 0) { return 3; }
    let b3: i32 = (__heap_bump_bytes() as i32);
    // Doubling the rounds must not double the high-water mark.
    if ((b3 - b2) > (b2 - b1) + 4096) { return 4; }
    return 42;
}
`

func TestMapCowColumnOwnershipBoundedX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapCowColumnOwnershipBoundedProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (4 = the COW copy's column claim is unbounded)", got)
	}
}

func TestMapCowColumnOwnershipBoundedWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapCowColumnOwnershipBoundedProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (4 = the COW copy's column claim is unbounded)", got)
	}
}

func TestMapCowColumnOwnershipBoundedArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapCowColumnOwnershipBoundedProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (4 = the COW copy's column claim is unbounded)", got)
	}
}

const mapInlineKeyColumnOwnershipProg = `
import "core/map";

struct S { m: Map[string, i32], n: i32 }

function ins(s: S, k: string, v: i32): S {
    let m: Map[string, i32] = s.m;
    m = m.insert(k, v);
    return S { m: m, n: s.n + 1 };
}

function get(s: S, k: string): i32 {
    match (s.m.get(k)) { Some(v) => { return v; }, None => { return 0 - 1; } }
}

function main(): i32 {
    let m0: Map[string, i32] = map_new(4);
    let s: S = S { m: m0, n: 0 };
    s = ins(s, "alpha", 1);
    s = ins(s, "beta", 2);
    s = ins(s, "gamma", 3);
    // A heap key in the same column: the claim still has to fire for it.
    s = ins(s, "a-key-far-past-the-inline-cap", 4);
    s = ins(s, "delta", 5);
    if (s.n != 5) { return 1; }
    if (s.m.len() != 5) { return 2; }
    if (get(s, "alpha") != 1) { return 3; }
    if (get(s, "beta") != 2) { return 4; }
    if (get(s, "gamma") != 3) { return 5; }
    if (get(s, "a-key-far-past-the-inline-cap") != 4) { return 6; }
    if (get(s, "delta") != 5) { return 7; }
    if (get(s, "missing") != 0 - 1) { return 8; }
    let keys: string[] = s.m.keys();
    if (keys.len() != 5) { return 9; }
    if (keys[0] != "alpha") { return 10; }
    if (keys[3] != "a-key-far-past-the-inline-cap") { return 11; }
    return 42;
}
`

func TestMapInlineKeyColumnOwnershipInterp(t *testing.T) {
	if got := runInterpExit(t, mapInlineKeyColumnOwnershipProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapInlineKeyColumnOwnershipX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapInlineKeyColumnOwnershipProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestMapInlineKeyColumnOwnershipWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapInlineKeyColumnOwnershipProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestMapInlineKeyColumnOwnershipArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapInlineKeyColumnOwnershipProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

const mapInlineKeyColumnOwnershipRcProg = `
import "core/map";
import "std/i32";

struct S { m: Map[string, i32], n: i32 }

function ins(s: S, k: string, v: i32): S {
    let m: Map[string, i32] = s.m;
    m = m.insert(k, v);
    return S { m: m, n: s.n + 1 };
}

function main(): i32 {
    let m0: Map[string, i32] = map_new(8);
    let s: S = S { m: m0, n: 0 };
    s = ins(s, "alpha", 1);
    s = ins(s, "beta", 2);
    let i: i32 = 0;
    while (i < 16) {
        s = ins(s, "k" + i.to_string(), i);
        i = i + 1;
    }
    if (s.m.len() != 18) { return 1; }
    return 42 + __rc_underflow_count();
}
`

func TestMapInlineKeyColumnOwnershipNoUnderflowX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapInlineKeyColumnOwnershipRcProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (%d rc underflows)", got, got-42)
	}
}

func TestMapInlineKeyColumnOwnershipNoUnderflowWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapInlineKeyColumnOwnershipRcProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (%d rc underflows)", got, got-42)
	}
}

func TestMapInlineKeyColumnOwnershipNoUnderflowArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapInlineKeyColumnOwnershipRcProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (%d rc underflows)", got, got-42)
	}
}
