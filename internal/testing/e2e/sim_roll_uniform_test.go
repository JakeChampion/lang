package e2e

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
    let d: sim.Sim = sim.new(seed);
    let fs: async.Future[i32][] = [
        sim.future_at(d, 5000000, 0),
        sim.future_at(d, 5000000, 1),
        sim.future_at(d, 5000000, 2)
    ];
    let (w, v) = async.race_on(d, fs, -1);
    // The winner's index and its value must agree, or the draw is not what
    // is being measured.
    if (w != v) { return 0 - 1; }
    return w;
}

function main(): i32 {
    let trials: i32 = 600;
    let counts: i32[] = [0, 0, 0];
    let s: i64 = 1;
    while (s <= (trials as i64)) {
        let w: i32 = tie3(s);
        if (w < 0 || w > 2) { return 1; }
        counts = counts.with(w, counts[w] + 1);
        s = s + 1;
    }
    if (counts[0] + counts[1] + counts[2] != trials) { return 2; }

    // Even split is 200 per bucket; allow +/- 25% (150..250). A mapping that
    // collapsed to a constant puts 600 in one bucket; one that lost a bit of
    // entropy puts ~300 in each of two.
    let i: i32 = 0;
    while (i < 3) {
        if (counts[i] < 150) { return 3; }
        if (counts[i] > 250) { return 4; }
        i = i + 1;
    }

    // Determinism is unaffected by any of the above: the same seed must still
    // give the same winner. This is the contract sim exists to provide, and it
    // is the thing a debiasing change must NOT break.
    let k: i64 = 1;
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
