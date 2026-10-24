package e2e

import "testing"

const randomUnbiasedProg = `
import "std/math" as math;

function main(): i32 {
    // Containment across range widths that straddle the mapping boundaries.
    let widths: i32[] = [1, 2, 3, 5, 7, 8, 16, 17, 100, 255, 256, 1000, 65536];
    let w: i32 = 0;
    while (w < widths.len()) {
        let width: i32 = widths[w];
        let t: i32 = 0;
        while (t < 300) {
            let v: i32 = math.random_int(10, 10 + width);
            if (v < 10 || v >= 10 + width) { return 1; }
            t = t + 1;
        }
        w = w + 1;
    }

    // Coverage: every bucket of a small range must actually be produced.
    let seen: i32[] = [0, 0, 0, 0, 0];
    let k: i32 = 0;
    while (k < 4000) {
        let v2: i32 = math.random_int(0, 5);
        seen = seen.with(v2, seen[v2] + 1);
        k = k + 1;
    }
    let s: i32 = 0;
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
    let single: i32 = 0;
    while (single < 50) {
        if (math.random_int(7, 8) != 7) { return 6; }
        single = single + 1;
    }

    // Negative and wide ranges stay in bounds.
    let n: i32 = 0;
    while (n < 200) {
        let v3: i32 = math.random_int(0 - 100, 100);
        if (v3 < (0 - 100) || v3 >= 100) { return 7; }
        let v4: i32 = math.random_int(0, 2000000000);
        if (v4 < 0 || v4 >= 2000000000) { return 8; }
        n = n + 1;
    }

    // Widest representable range: the width wraps to 2^32-1, the largest
    // __random_below can be asked for and the one with a non-trivial
    // rejection threshold. Reaching here at all also proves termination.
    let wide: i32 = 0;
    while (wide < 100) {
        let v5: i32 = math.random_int((0 - 2147483647) - 1, 2147483647);
        if (v5 == 2147483647) { return 9; }
        wide = wide + 1;
    }

    return 42;
}
`

const randomUnbiasedModelProg = `
function main(): i32 {
    let W: u64 = 256 as u64;

    let range: i32 = 1;
    while (range < 40) {
        let r: u64 = range as u64;
        let threshold: u64 = (W - r) % r;      // == W mod r, the surplus zone

        // counts[b] = how many accepted draws land in bucket b.
        let counts: i32[] = [];
        let c: i32 = 0;
        while (c < range) { counts = counts.append(0); c = c + 1; }

        let rejected: i32 = 0;
        let u: i32 = 0;
        while (u < 256) {
            let product: u64 = (u as u64) * r;
            let low: u64 = product % W;
            let high: u64 = product / W;
            if (low < threshold) {
                rejected = rejected + 1;
            } else {
                let b: i32 = high as i32;
                if (b < 0 || b >= range) { return 1; }   // mapping escaped [0, range)
                counts = counts.with(b, counts[b] + 1);
            }
            u = u + 1;
        }

        // Exactly W mod range draws are rejected.
        let expect_rej: i32 = (W % r) as i32;
        if (rejected != expect_rej) { return 2; }

        // Every bucket reachable, and all counts identical -- uniform.
        let first: i32 = counts[0];
        if (first == 0) { return 3; }
        let i: i32 = 1;
        while (i < range) {
            if (counts[i] != first) { return 4; }
            i = i + 1;
        }

        range = range + 1;
    }

    // Contrast: the OLD biased mapping (u % range) is measurably skewed for a
    // range that does not divide the word size. This arm fails if the fix is
    // reverted, so the test cannot silently pass on a regression.
    let biased: i32[] = [0, 0, 0];
    let u2: i32 = 0;
    while (u2 < 256) {
        let slot: i32 = u2 % 3;
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
