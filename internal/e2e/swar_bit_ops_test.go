package e2e

// Differential coverage for the SWAR bit-counting primitives in std/i32,
// std/i64, std/u32 and std/u64.
//
// count_ones / leading_zeros / trailing_zeros were 32- and 64-iteration
// software loops (the source comment said "no intrinsics surface in lang").
// They are now branchless SWAR: the count accumulates in-place in 2-, then 4-,
// then 8-bit fields of the word itself. This program checks all twelve
// functions against the loop implementations they replaced — the references
// are recomputed inside the program, so it is a true differential rather than
// a table of expected answers.
//
// Coverage is aimed where bit tricks break: every 1<<k and its neighbours
// (1<<k)-1 and (1<<k)+1 for all k, at both widths; 3000 pseudo-random values
// including negatives so the sign bit is set; and the zero / all-ones
// boundaries, where smear-and-count and the x^(x-1) identity are least
// obviously correct. Returns 42 iff every case agrees.

import "testing"

const swarBitOpsProg = `
import "std/i32";
import "std/i64";
import "std/u32";
import "std/u64";

// The loop implementations these replaced, kept here as references.
function ref_pop32(n: u32): i32 {
    var u: u32 = n;
    var c: i32 = 0;
    var i: i32 = 0;
    while (i < 32) {
        if ((u & (1 as u32)) != (0 as u32)) { c = c + 1; }
        u = u >> (1 as u32);
        i = i + 1;
    }
    return c;
}

function ref_clz32(n: u32): i32 {
    if (n == (0 as u32)) { return 32; }
    var u: u32 = n;
    var top: u32 = (1 as u32) << (31 as u32);
    var c: i32 = 0;
    while ((u & top) == (0 as u32)) { c = c + 1; u = u << (1 as u32); }
    return c;
}

function ref_ctz32(n: u32): i32 {
    if (n == (0 as u32)) { return 32; }
    var u: u32 = n;
    var c: i32 = 0;
    while ((u & (1 as u32)) == (0 as u32)) { c = c + 1; u = u >> (1 as u32); }
    return c;
}

function ref_pop64(n: u64): i32 {
    var u: u64 = n;
    var c: i32 = 0;
    var i: i32 = 0;
    while (i < 64) {
        if ((u & (1 as u64)) != (0 as u64)) { c = c + 1; }
        u = u >> (1 as u64);
        i = i + 1;
    }
    return c;
}

function ref_clz64(n: u64): i32 {
    if (n == (0 as u64)) { return 64; }
    var u: u64 = n;
    var top: u64 = (1 as u64) << (63 as u64);
    var c: i32 = 0;
    while ((u & top) == (0 as u64)) { c = c + 1; u = u << (1 as u64); }
    return c;
}

function ref_ctz64(n: u64): i32 {
    if (n == (0 as u64)) { return 64; }
    var u: u64 = n;
    var c: i32 = 0;
    while ((u & (1 as u64)) == (0 as u64)) { c = c + 1; u = u >> (1 as u64); }
    return c;
}

function nextr(x: i32): i32 {
    var v: i32 = x;
    v = v ^ (v << 13);
    v = v ^ (v >> 17);
    v = v ^ (v << 5);
    return v;
}

function main(): i32 {
    // Every 1<<k and its neighbours, 32-bit, signed and unsigned views.
    var k: i32 = 0;
    while (k < 32) {
        var bit: u32 = (1 as u32) << (k as u32);
        var vals: u32[] = [bit, bit - (1 as u32), bit + (1 as u32)];
        var q: i32 = 0;
        while (q < 3) {
            var v: u32 = vals[q];
            if (v.count_ones() != ref_pop32(v)) { return 1; }
            if (v.leading_zeros() != ref_clz32(v)) { return 2; }
            if (v.trailing_zeros() != ref_ctz32(v)) { return 3; }
            var s: i32 = v as i32;
            if (s.count_ones() != ref_pop32(v)) { return 4; }
            if (s.leading_zeros() != ref_clz32(v)) { return 5; }
            if (s.trailing_zeros() != ref_ctz32(v)) { return 6; }
            q = q + 1;
        }
        k = k + 1;
    }

    // Same, 64-bit.
    var k6: i32 = 0;
    while (k6 < 64) {
        var bit6: u64 = (1 as u64) << (k6 as u64);
        var vals6: u64[] = [bit6, bit6 - (1 as u64), bit6 + (1 as u64)];
        var q6: i32 = 0;
        while (q6 < 3) {
            var v6: u64 = vals6[q6];
            if (v6.count_ones() != ref_pop64(v6)) { return 7; }
            if (v6.leading_zeros() != ref_clz64(v6)) { return 8; }
            if (v6.trailing_zeros() != ref_ctz64(v6)) { return 9; }
            var s6: i64 = v6 as i64;
            if (s6.count_ones() != ref_pop64(v6)) { return 10; }
            if (s6.leading_zeros() != ref_clz64(v6)) { return 11; }
            if (s6.trailing_zeros() != ref_ctz64(v6)) { return 12; }
            q6 = q6 + 1;
        }
        k6 = k6 + 1;
    }

    // Pseudo-random sweep, including negatives (sign bit set).
    var seed: i32 = 987654321;
    var t: i32 = 0;
    while (t < 3000) {
        seed = nextr(seed);
        var u: u32 = seed as u32;
        if (u.count_ones() != ref_pop32(u)) { return 13; }
        if (u.leading_zeros() != ref_clz32(u)) { return 14; }
        if (u.trailing_zeros() != ref_ctz32(u)) { return 15; }
        if (seed.count_ones() != ref_pop32(u)) { return 16; }
        if (seed.leading_zeros() != ref_clz32(u)) { return 17; }
        if (seed.trailing_zeros() != ref_ctz32(u)) { return 18; }

        // Two draws so the 64-bit high half varies independently.
        var hi: i32 = nextr(seed);
        var lowmask: u64 = ((1 as u64) << (32 as u64)) - (1 as u64);
        var w: u64 = ((hi as u64) << (32 as u64)) | ((u as u64) & lowmask);
        if (w.count_ones() != ref_pop64(w)) { return 19; }
        if (w.leading_zeros() != ref_clz64(w)) { return 20; }
        if (w.trailing_zeros() != ref_ctz64(w)) { return 21; }
        var sw: i64 = w as i64;
        if (sw.count_ones() != ref_pop64(w)) { return 22; }
        if (sw.leading_zeros() != ref_clz64(w)) { return 23; }
        if (sw.trailing_zeros() != ref_ctz64(w)) { return 24; }
        t = t + 1;
    }

    // Boundaries: zero (nothing to smear) and all-ones (no zeros to find).
    if ((0 as u32).count_ones() != 0) { return 25; }
    if ((0 as u32).leading_zeros() != 32) { return 26; }
    if ((0 as u32).trailing_zeros() != 32) { return 27; }
    if ((0 as u64).count_ones() != 0) { return 28; }
    if ((0 as u64).leading_zeros() != 64) { return 29; }
    if ((0 as u64).trailing_zeros() != 64) { return 30; }
    if ((0 - 1).count_ones() != 32) { return 31; }
    if ((0 - 1).leading_zeros() != 0) { return 32; }
    if ((0 - 1).trailing_zeros() != 0) { return 33; }
    if (((0 as i64) - (1 as i64)).count_ones() != 64) { return 34; }

    // count_zeros / bit_length ride on the same primitives.
    if ((0 as u32).count_zeros() != 32) { return 35; }
    if ((255 as u32).bit_length() != 8) { return 36; }
    if ((256 as u32).bit_length() != 9) { return 37; }

    return 42;
}
`

func TestSwarBitOpsInterp(t *testing.T) {
	if got := runInterpExit(t, swarBitOpsProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestSwarBitOpsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, swarBitOpsProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestSwarBitOpsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, swarBitOpsProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestSwarBitOpsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, swarBitOpsProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
