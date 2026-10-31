package e2e

import "testing"

const simRollUniformProg = `
import "std/time";
import "std/async";
import "std/sim";
import "std/i32";
import "std/i64";
import "std/string";

// A three-way tie: three tasks wait for the same virtual instant, so the task
// that runs first is entirely the seeded draw.
function arrive(d: sim.Sim, at: Instant, first: Cell[i32], me: i32): i32 {
    let moved: boolean = d.advance_to(at);
    if (first.get() < 0) {
        first.set(me);
    }
    return me;
}

function tie3(seed: i64): i32 {
    let d: sim.Sim = sim.new(seed);
    let first: Cell[i32] = cell_new(0 - 1);
    let at: Instant = sim.at(time.duration_nanos(5000000));
    let entries: (() => i32)[] = [
        (): i32 => arrive(d, at, first, 0),
        (): i32 => arrive(d, at, first, 1),
        (): i32 => arrive(d, at, first, 2)
    ];
    let st: async.TaskStatus[i32][] = sim.run_tasks(d, entries, [None, None, None]);
    return first.get();
}

function main(): i32 {
    let trials: i32 = 600;
    let counts: i32[] = [0, 0, 0];
    for s in 1..trials + 1 {
        let w: i32 = tie3(s as i64);
        if (w < 0 || w > 2) { return 1; }
        counts = counts.with(w, counts[w] + 1);
    }
    if (counts[0] + counts[1] + counts[2] != trials) { return 2; }

    // Even split is 200 per bucket; allow +/- 25% (150..250). A mapping that
    // collapsed to a constant puts 600 in one bucket; one that lost a bit of
    // entropy puts ~300 in each of two.
    for i in 0..3 {
        if (counts[i] < 150) { return 3; }
        if (counts[i] > 250) { return 4; }
    }

    // Determinism is unaffected by any of the above: the same seed must still
    // give the same winner. This is the contract sim exists to provide, and it
    // is the thing a debiasing change must NOT break.
    for k in 1..51 {
        if (tie3(k as i64) != tie3(k as i64)) { return 5; }
    }

    return 42;
}
`

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
