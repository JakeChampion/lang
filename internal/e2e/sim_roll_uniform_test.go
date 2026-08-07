package e2e

// std/sim's `__roll` maps its PRNG onto a range. A Park-Miller step mapped
// with a bare `x % n` is biased toward small values whenever `n` does not
// divide the generator's range, and reads exactly the low bits an
// LCG is worst at. It now takes one PCG32 step from std/rand and maps it with
// Lemire's multiply-shift-and-reject (#6193).
//
// WHAT THIS DOES NOT GUARD, stated up front because it is the obvious reading
// of the name: it does NOT distinguish the old biased mapping from the new
// one. Measured — the old Park-Miller + `x % n` implementation passes this
// test unchanged. It cannot fail: `__roll`'s bias is proportional to n/range,
// and `__roll` is private with only two call sites, the larger of which passes
// n=100 against a 2^31 range. That is a skew of ~5e-8, orders of magnitude
// below what any runnable sample size resolves. The bias was real and worth
// removing; it is simply not observable through a public surface, so no test
// can pin it and this one does not pretend to.
//
// What it DOES guard is the regression class that is observable: a mapping
// that collapses (always returns 0 — e.g. an argument swapped at the
// `rng_below` call), that skews grossly, or that stops being a function of the
// seed. None of those were covered before: the existing sim tests pin GOLDEN
// SEQUENCES, and a collapsed mapping produces a perfectly stable golden
// sequence.
//
// `__roll` is private, so the draws are taken through the one public surface
// that exposes them: a race between N futures ready at the same virtual
// instant, whose winner is a `__roll(ties.len())`. Over many seeds each slot
// should win about equally often.
//
// The bar is deliberately loose (each bucket within 25% of even for a 3-way
// tie over 600 seeds). A tight bound on a fixed seed range would be a golden
// value wearing a statistic's clothes, and would go red on any future
// generator change for no good reason.

import "testing"

const simRollUniformProg = `
import "std/async";
import "std/sim";
import "std/i32";
import "std/i64";
import "std/string";

// A three-way tie: every future is ready at the same virtual instant, so the
// winner is entirely the seeded draw.
function tie3(seed: i64): i32 {
    var d: sim.Sim = sim.new(seed);
    var fs: async.Future[i32][] = [
        sim.future_at(d, 5000000, 0),
        sim.future_at(d, 5000000, 1),
        sim.future_at(d, 5000000, 2)
    ];
    var (w, v) = async.race_on(d, fs, -1);
    // The winner's index and its value must agree, or the draw is not what
    // is being measured.
    if (w != v) { return 0 - 1; }
    return w;
}

function main(): i32 {
    var trials: i32 = 600;
    var counts: i32[] = [0, 0, 0];
    var s: i64 = 1;
    while (s <= (trials as i64)) {
        var w: i32 = tie3(s);
        if (w < 0 || w > 2) { return 1; }
        counts = counts.with(w, counts[w] + 1);
        s = s + 1;
    }
    if (counts[0] + counts[1] + counts[2] != trials) { return 2; }

    // Even split is 200 per bucket; allow +/- 25% (150..250). A mapping that
    // collapsed to a constant puts 600 in one bucket; one that lost a bit of
    // entropy puts ~300 in each of two.
    var i: i32 = 0;
    while (i < 3) {
        if (counts[i] < 150) { return 3; }
        if (counts[i] > 250) { return 4; }
        i = i + 1;
    }

    // Determinism is unaffected by any of the above: the same seed must still
    // give the same winner. This is the contract sim exists to provide, and it
    // is the thing a debiasing change must NOT break.
    var k: i64 = 1;
    while (k <= 50) {
        if (tie3(k) != tie3(k)) { return 5; }
        k = k + 1;
    }

    return 42;
}
`

func TestSimRollUniformInterp(t *testing.T) {
	if got := runInterpExit(t, simRollUniformProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestSimRollUniformX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, simRollUniformProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestSimRollUniformArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, simRollUniformProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
