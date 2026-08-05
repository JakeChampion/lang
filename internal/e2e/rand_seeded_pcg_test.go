package e2e

// Coverage for std/rand's seeded PCG32 generator and the `*_seeded` helpers.
//
// `shuffle` / `choice` / `sample` draw from the OS CSPRNG — one syscall per
// draw, which is the right default but costs ~5x in bulk. The seeded siblings
// run in userspace from a seed, which also makes a run reproducible.
//
// The generator is pinned to PCG32's actual output, not merely to "looks
// random": the first six u32s for seed 42 are compared against values
// computed independently from the PCG32 definition. That catches a wrong
// multiplier, a mis-ordered xorshift, or a rotate that silently loses bits —
// none of which a distribution check would notice.
//
// The rotate is the subtle part and gets its own attention. `rotr32(v, 0)`
// must return `v`; a naive implementation computes `v << 32`, a shift by the
// full width, which is undefined rather than zero. A rotation of 0 happens
// whenever the state's top 5 bits are zero, i.e. 1 draw in 32, so the vector
// check above hits it quickly.

import "testing"

const randSeededPcgProg = `
import "std/option";
import "std/rand" as rand;
import "std/i32";
import "std/i64";

function main(): i32 {
    // PCG32 reference vectors for seed 42, computed independently from the
    // algorithm definition (shown here as i32 bit patterns).
    var want: i32[] = [0 - 1024099370, 1795671209, 1924641435, 1143034755, 0 - 173056339, 1757328946];
    var st: i64 = rand.rng_seed(42 as i64);
    var i: i32 = 0;
    while (i < want.len()) {
        var step = rand.rng_next(st);
        st = step.0;
        if ((step.1 as i32) != want[i]) { return 1; }
        i = i + 1;
    }

    // Equal seeds reproduce the sequence exactly.
    var sa: i64 = rand.rng_seed(7 as i64);
    var sb: i64 = rand.rng_seed(7 as i64);
    var t: i32 = 0;
    while (t < 50) {
        var pa = rand.rng_next(sa);
        var pb = rand.rng_next(sb);
        sa = pa.0;
        sb = pb.0;
        if ((pa.1 as i32) != (pb.1 as i32)) { return 2; }
        t = t + 1;
    }

    // Different seeds diverge (allowing a couple of chance collisions).
    var sc: i64 = rand.rng_seed(8 as i64);
    var sd: i64 = rand.rng_seed(9 as i64);
    var same: i32 = 0;
    var u: i32 = 0;
    while (u < 20) {
        var pc = rand.rng_next(sc);
        var pd = rand.rng_next(sd);
        sc = pc.0;
        sd = pd.0;
        if ((pc.1 as i32) == (pd.1 as i32)) { same = same + 1; }
        u = u + 1;
    }
    if (same > 2) { return 3; }

    // rng_below: containment and rough uniformity over 5000 draws.
    var s2: i64 = rand.rng_seed(12345 as i64);
    var seen: i32[] = [0, 0, 0, 0, 0];
    var k: i32 = 0;
    while (k < 5000) {
        var bstep = rand.rng_below(s2, 5);
        s2 = bstep.0;
        if (bstep.1 < 0 || bstep.1 >= 5) { return 4; }
        seen = seen.with(bstep.1, seen[bstep.1] + 1);
        k = k + 1;
    }
    var s: i32 = 0;
    while (s < 5) {
        if (seen[s] < 800 || seen[s] > 1200) { return 5; }
        s = s + 1;
    }
    // Degenerate bounds leave the state alone and return 0.
    if (rand.rng_below(s2, 0).1 != 0) { return 6; }
    if (rand.rng_below(s2, 0 - 3).1 != 0) { return 7; }
    if (rand.rng_below(s2, 0).0 != s2) { return 8; }
    if (rand.rng_below(s2, 1).1 != 0) { return 9; }

    // rng_between
    var s3: i64 = rand.rng_seed(999 as i64);
    var m: i32 = 0;
    while (m < 500) {
        var wstep = rand.rng_between(s3, 0 - 10, 10);
        s3 = wstep.0;
        if (wstep.1 < (0 - 10) || wstep.1 >= 10) { return 10; }
        m = m + 1;
    }
    if (rand.rng_between(s3, 5, 5).1 != 5) { return 11; }
    if (rand.rng_between(s3, 9, 3).1 != 9) { return 12; }

    // shuffle_seeded: a true permutation, reproducible, input untouched.
    var xs: i32[] = [];
    var x: i32 = 0;
    while (x < 40) { xs = xs.append(x); x = x + 1; }
    var p1: i32[] = rand.shuffle_seeded(2024 as i64, xs);
    var p2: i32[] = rand.shuffle_seeded(2024 as i64, xs);
    if (p1.len() != 40) { return 13; }
    var fixed: i32 = 0;
    var q: i32 = 0;
    while (q < 40) {
        if (p1[q] != p2[q]) { return 14; }        // same seed -> same permutation
        if (p1[q] == xs[q]) { fixed = fixed + 1; }
        q = q + 1;
    }
    if (fixed > 12) { return 15; }                 // not the identity
    // Every element present exactly once.
    var mark: i32[] = [];
    var z: i32 = 0;
    while (z < 40) { mark = mark.append(0); z = z + 1; }
    var y: i32 = 0;
    while (y < 40) { mark = mark.with(p1[y], mark[p1[y]] + 1); y = y + 1; }
    var c2: i32 = 0;
    while (c2 < 40) {
        if (mark[c2] != 1) { return 16; }
        c2 = c2 + 1;
    }
    // Value semantics: the input array is unchanged.
    var o: i32 = 0;
    while (o < 40) {
        if (xs[o] != o) { return 17; }
        o = o + 1;
    }
    // A different seed gives a different permutation.
    var p3: i32[] = rand.shuffle_seeded(2025 as i64, xs);
    var identical: boolean = true;
    var w2: i32 = 0;
    while (w2 < 40) {
        if (p3[w2] != p1[w2]) { identical = false; }
        w2 = w2 + 1;
    }
    if (identical) { return 18; }
    // Empty and single-element inputs.
    var none: i32[] = [];
    if (rand.shuffle_seeded(1 as i64, none).len() != 0) { return 19; }
    var one: i32[] = [9];
    var so: i32[] = rand.shuffle_seeded(1 as i64, one);
    if (so.len() != 1 || so[0] != 9) { return 20; }

    // choice_seeded / sample_seeded
    if (rand.choice_seeded(5 as i64, xs).is_none()) { return 21; }
    if (rand.choice_seeded(5 as i64, none).is_some()) { return 22; }
    if (rand.sample_seeded(5 as i64, xs, 7).len() != 7) { return 23; }
    if (rand.sample_seeded(5 as i64, xs, 0).len() != 0) { return 24; }
    if (rand.sample_seeded(5 as i64, xs, 100).len() != 40) { return 25; }
    // sample_seeded draws WITHOUT replacement: 7 distinct elements.
    var smp: i32[] = rand.sample_seeded(11 as i64, xs, 7);
    var dup: i32[] = [];
    var d0: i32 = 0;
    while (d0 < 40) { dup = dup.append(0); d0 = d0 + 1; }
    var d1: i32 = 0;
    while (d1 < smp.len()) {
        if (dup[smp[d1]] != 0) { return 26; }
        dup = dup.with(smp[d1], 1);
        d1 = d1 + 1;
    }

    return 42;
}
`

func TestRandSeededPcgInterp(t *testing.T) {
	if got := runInterpExit(t, randSeededPcgProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestRandSeededPcgX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, randSeededPcgProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestRandSeededPcgWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, randSeededPcgProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestRandSeededPcgArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, randSeededPcgProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
