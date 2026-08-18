package e2e

// A Map COW copy's claim on the columns it copied (#6242).
//
// `__map_cow_inplace` and `__map_clone` produce the fresh buffer with one
// `__memcpy`, which is shallow over the pointer-shaped key and value columns.
// Both handles then reach rc==1 independently, so BOTH deep drops run — and
// whichever fires first takes the other's memory with it. What that looked
// like depended on the string ABI:
//
//	arm64 / wasm (two-word)  -> the key slot is an unrefcounted 16-byte cell
//	                            that __drop_map_str_keys frees outright, so the
//	                            second free handed the same cell back out. The
//	                            source map then probed against recycled memory:
//	                            a wrong answer on arm64-darwin, a HANG on wasm
//	                            (the probe loop never finds its key and never
//	                            terminates), SIGSEGV on arm64-linux.
//	x86-64 (single-word)     -> the slot is the string data pointer and the two
//	                            drops double-dec it. Survivable, and survival is
//	                            why this went unnoticed.
//
// The programs below therefore drop one side of a COW copy while the other is
// still live, which nothing else in the suite did. A value assertion alone is
// not enough — the freed block still holds its old bytes for a while — so the
// rc probe and the boundedness probe are here as the direct measurements.

import "testing"

// The copy dies while the source is live, and the source's key must survive.
// Long key so the bytes are heap-allocated rather than inline, `x + i` inserts
// so the COW branch is genuinely taken every round.
const mapCowColumnOwnershipProg = `
import "core/map";
import "std/i32";

function main(): i32 {
    var a: Map[string, i32] = map_new(8);
    a = a.insert("a-fairly-long-key-that-heap-allocates", 7);
    var i: i32 = 0;
    while (i < 8) {
        var b = a;
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
    var v: Map[string, i32[]] = map_new(8);
    v = v.insert("vk-a-fairly-long-key", [11, 22, 33]);
    var j: i32 = 0;
    while (j < 8) {
        var w = v;
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
    var s: Map[string, i32] = map_new(8);
    s = s.insert("shared-key-that-heap-allocates", 1);
    var c = s;
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

// The rc probe — the over-release direction, which the value assertions above
// only catch once the freed block has actually been recycled into something
// else. `__rc_underflow_count()` is a compiled-backend probe (the interpreter
// has no rc runtime), so this leg is compiled-only.
const mapCowColumnOwnershipRcProg = `
import "core/map";
import "std/i32";

function main(): i32 {
    var a: Map[string, i32] = map_new(8);
    a = a.insert("a-fairly-long-key-that-heap-allocates", 7);
    var i: i32 = 0;
    while (i < 16) {
        var b = a;
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

// The other direction. A claim that nothing releases is a leak, and the rebox
// half allocates a cell per entry per copy — so a COW loop that was bounded
// before has to stay bounded. __heap_bump_bytes() is the high-water mark the
// freelist could not recycle, which is host-independent (RSS is not — see
// docs/LOCAL-DEV-LOOP.md).
//
// The map carries several entries so the per-entry cost is what is measured,
// not a single slot's.
const mapCowColumnOwnershipBoundedProg = `
import "core/map";
import "std/i32";

function churn(rounds: i32): i32 {
    var a: Map[string, i32] = map_new(16);
    var s: i32 = 0;
    while (s < 6) { a = a.insert("seed-key-that-heap-allocates" + s.to_string(), s); s = s + 1; }
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < rounds) {
        var b = a;
        b = b.insert("x" + i.to_string(), i);
        acc = (acc + b.len()) % 251;
        i = i + 1;
    }
    return acc;
}

function main(): i32 {
    if (churn(50) < 0) { return 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    if (churn(400) < 0) { return 2; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (churn(800) < 0) { return 3; }
    var b3: i32 = (__heap_bump_bytes() as i32);
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

// The inline (SSO) half of the same claim (#7081). On a two-word string ABI
// the inline flag is the top bit of the cell's `len` word and `data` holds the
// key's own characters, so a claim that inc's `data` unconditionally
// dereferences those characters as an address: wasmtime trapped at
// 0x68706c59, which is `"alph"` packed little-endian minus the rc word's 8.
// x86-64 escaped it because its single-word inline form is bit-0 tagged and
// __fern_rc_inc skips odd pointers.
//
// The keys here are all <= 7 bytes, so every one of them is inline on wasm32,
// and "beta" is here because its first byte is EVEN — an alignment-shaped
// guard passes "alpha" (0x61, odd) and still walks into "beta".
//
// The shape is the issue's: a Map field on a state struct, read out, mutated,
// and stored back into a fresh struct, which is what makes the second insert
// find the handle aliased and take the COW copy.
const mapInlineKeyColumnOwnershipProg = `
import "core/map";

struct S { m: Map[string, i32], n: i32 }

function ins(s: S, k: string, v: i32): S {
    var m: Map[string, i32] = s.m;
    m = m.insert(k, v);
    return S { m: m, n: s.n + 1 };
}

function get(s: S, k: string): i32 {
    match (s.m.get(k)) { Some(v) => { return v; }, None => { return 0 - 1; } }
}

function main(): i32 {
    var m0: Map[string, i32] = map_new(4);
    var s: S = S { m: m0, n: 0 };
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
    var keys: string[] = s.m.keys();
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

// Skipping the inline key's inc must not leave its drop over-releasing: the
// column's __fern_str_dec is inline-aware on the same terms, so the pair still
// has to balance.
const mapInlineKeyColumnOwnershipRcProg = `
import "core/map";
import "std/i32";

struct S { m: Map[string, i32], n: i32 }

function ins(s: S, k: string, v: i32): S {
    var m: Map[string, i32] = s.m;
    m = m.insert(k, v);
    return S { m: m, n: s.n + 1 };
}

function main(): i32 {
    var m0: Map[string, i32] = map_new(8);
    var s: S = S { m: m0, n: 0 };
    s = ins(s, "alpha", 1);
    s = ins(s, "beta", 2);
    var i: i32 = 0;
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
