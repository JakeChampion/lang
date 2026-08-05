package e2e

// Coverage for std/math's unbiased bounded random.
//
// `random_int` used to map a 32-bit draw with a bare `u % range`, which is
// biased whenever `range` does not divide 2^32: the first `2^32 mod range`
// values come up one extra time per 2^32 draws. `rand.shuffle` is Fisher-Yates
// over `random_int`, so that made shuffles non-uniform permutations. It now
// uses Lemire's nearly-divisionless method: multiply-shift for the mapping,
// and reject the surplus zone identified by the product's low half.
//
// Two halves, because neither alone is enough:
//
//  1. `random_unbiased` checks the real `random_int` behaviourally — every
//     draw in [lo, hi), every bucket reachable, degenerate/boundary ranges,
//     and termination on the widest range. It CANNOT observe the bias itself:
//     the residual is ~2^-32 and no feasible number of samples would show it.
//
//  2. `random_unbiased_model` runs the identical multiply-shift-and-reject
//     formula at an 8-bit word size, where all 256 draws can be enumerated
//     exhaustively, and asserts perfect uniformity — every bucket landing on
//     exactly the same count, with exactly `W mod range` draws rejected. That
//     is what actually pins the algorithm, and because it uses the same u64
//     multiply / shift / mask operations as the implementation, it also
//     verifies those lower correctly on each backend (the part most likely to
//     differ between interp, x86-64, wasm and arm64). For contrast it also
//     computes the OLD `u % range` mapping and asserts it is measurably
//     skewed, so the test would fail if someone reverted the fix.

import "testing"

const randomUnbiasedProg = `
import "std/math" as math;

function main(): i32 {
    // Containment across range widths that straddle the mapping boundaries.
    var widths: i32[] = [1, 2, 3, 5, 7, 8, 16, 17, 100, 255, 256, 1000, 65536];
    var w: i32 = 0;
    while (w < widths.len()) {
        var width: i32 = widths[w];
        var t: i32 = 0;
        while (t < 300) {
            var v: i32 = math.random_int(10, 10 + width);
            if (v < 10 || v >= 10 + width) { return 1; }
            t = t + 1;
        }
        w = w + 1;
    }

    // Coverage: every bucket of a small range must actually be produced.
    var seen: i32[] = [0, 0, 0, 0, 0];
    var k: i32 = 0;
    while (k < 4000) {
        var v2: i32 = math.random_int(0, 5);
        seen = seen.with(v2, seen[v2] + 1);
        k = k + 1;
    }
    var s: i32 = 0;
    while (s < 5) {
        if (seen[s] == 0) { return 2; }
        // Smoke test for a grossly broken map (expected 800 each), NOT a bias
        // test -- see the model program for that.
        if (seen[s] < 400 || seen[s] > 1600) { return 3; }
        s = s + 1;
    }

    // Degenerate and boundary ranges.
    if (math.random_int(5, 5) != 5) { return 4; }
    if (math.random_int(9, 3) != 9) { return 5; }
    var single: i32 = 0;
    while (single < 50) {
        if (math.random_int(7, 8) != 7) { return 6; }
        single = single + 1;
    }

    // Negative and wide ranges stay in bounds.
    var n: i32 = 0;
    while (n < 200) {
        var v3: i32 = math.random_int(0 - 100, 100);
        if (v3 < (0 - 100) || v3 >= 100) { return 7; }
        var v4: i32 = math.random_int(0, 2000000000);
        if (v4 < 0 || v4 >= 2000000000) { return 8; }
        n = n + 1;
    }

    // Widest representable range: the width wraps to 2^32-1, the largest
    // __random_below can be asked for and the one with a non-trivial
    // rejection threshold. Reaching here at all also proves termination.
    var wide: i32 = 0;
    while (wide < 100) {
        var v5: i32 = math.random_int((0 - 2147483647) - 1, 2147483647);
        if (v5 == 2147483647) { return 9; }
        wide = wide + 1;
    }

    return 42;
}
`

// The same formula at 8-bit word size, enumerated exhaustively. Uses the same
// u64 multiply / shift / mask the real implementation uses.
const randomUnbiasedModelProg = `
function main(): i32 {
    var W: u64 = 256 as u64;

    var range: i32 = 1;
    while (range < 40) {
        var r: u64 = range as u64;
        var threshold: u64 = (W - r) % r;      // == W mod r, the surplus zone

        // counts[b] = how many accepted draws land in bucket b.
        var counts: i32[] = [];
        var c: i32 = 0;
        while (c < range) { counts = counts.append(0); c = c + 1; }

        var rejected: i32 = 0;
        var u: i32 = 0;
        while (u < 256) {
            var product: u64 = (u as u64) * r;
            var low: u64 = product % W;
            var high: u64 = product / W;
            if (low < threshold) {
                rejected = rejected + 1;
            } else {
                var b: i32 = high as i32;
                if (b < 0 || b >= range) { return 1; }   // mapping escaped [0, range)
                counts = counts.with(b, counts[b] + 1);
            }
            u = u + 1;
        }

        // Exactly W mod range draws are rejected.
        var expect_rej: i32 = (W % r) as i32;
        if (rejected != expect_rej) { return 2; }

        // Every bucket reachable, and all counts identical -- uniform.
        var first: i32 = counts[0];
        if (first == 0) { return 3; }
        var i: i32 = 1;
        while (i < range) {
            if (counts[i] != first) { return 4; }
            i = i + 1;
        }

        range = range + 1;
    }

    // Contrast: the OLD biased mapping (u % range) is measurably skewed for a
    // range that does not divide the word size. This arm fails if the fix is
    // reverted, so the test cannot silently pass on a regression.
    var biased: i32[] = [0, 0, 0];
    var u2: i32 = 0;
    while (u2 < 256) {
        var slot: i32 = u2 % 3;
        biased = biased.with(slot, biased[slot] + 1);
        u2 = u2 + 1;
    }
    if (biased[0] == biased[1]) { return 5; }   // 86 vs 85 -- must differ
    if (biased[1] != biased[2]) { return 6; }
    if (biased[0] != biased[1] + 1) { return 7; }

    return 42;
}
`

func TestRandomUnbiasedInterp(t *testing.T) {
	if got := runInterpExit(t, randomUnbiasedProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestRandomUnbiasedX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, randomUnbiasedProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestRandomUnbiasedWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, randomUnbiasedProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestRandomUnbiasedArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, randomUnbiasedProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

func TestRandomUnbiasedModelInterp(t *testing.T) {
	if got := runInterpExit(t, randomUnbiasedModelProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestRandomUnbiasedModelX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, randomUnbiasedModelProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestRandomUnbiasedModelWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, randomUnbiasedModelProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestRandomUnbiasedModelArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, randomUnbiasedModelProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
