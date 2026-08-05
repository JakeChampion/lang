package e2e

// std/fuzz draws its mutations from a seeded PCG32 rather than the CSPRNG.
//
// Two things change. Mutation no longer costs a syscall per draw (up to two
// per mutation, 14 call sites). And a run is now REPRODUCIBLE: `fuzz_run`
// draws one seed from the CSPRNG, names it in the failure diagnostic, and
// `fuzz_run_seeded` replays that exact sequence — which is the difference
// between a fuzz failure you can debug and one you can only stare at.
//
// The determinism assertion is the point of this test: two `fuzz_run_seeded`
// calls with equal seeds must produce byte-identical diagnostics, including
// the iteration number, the mutation mode, and the offending input. That
// pins the whole chain — seeding, the threaded state through __fuzz_mutate,
// and the mutation strategy dispatch — not just "it returned a failure".

import "testing"

const fuzzSeededProg = `
import "std/fuzz" as fuzz;
import "std/test" as test;
import "std/i32";
import "std/i64";
import "std/string";

// Fails only on inputs containing 0xFF, so the mutation engine has to
// actually find one (mode 6 writes 0xFF; mode 0 can too).
function target(input: string): test.TestOutcome {
    var i: i32 = 0;
    while (i < input.len()) {
        if ((input[i] as i32) == 255) { return test.fail("found 0xFF"); }
        i = i + 1;
    }
    return test.pass();
}

function always_pass(input: string): test.TestOutcome { return test.pass(); }

function outcome_failed(o: test.TestOutcome): boolean {
    match (o) { Fail(m) => { return true; }, Pass => { return false; } }
}

function outcome_msg(o: test.TestOutcome): string {
    match (o) { Fail(m) => { return m; }, Pass => { return ""; } }
}

function main(): i32 {
    var seeds: string[] = ["abc", "hello", "xyzzy"];

    // Equal seeds replay identically -- same outcome AND same diagnostic.
    var a = fuzz.fuzz_run_seeded(seeds, 300, 12345 as i64, target);
    var b = fuzz.fuzz_run_seeded(seeds, 300, 12345 as i64, target);
    if (outcome_failed(a) != outcome_failed(b)) { return 1; }
    if (outcome_msg(a) != outcome_msg(b)) { return 2; }

    // This seed is expected to find a 0xFF, so the determinism check above
    // is comparing real diagnostics rather than two empty strings.
    if (!outcome_failed(a)) { return 3; }
    // The diagnostic names the seed, which is what makes it replayable.
    if (!outcome_msg(a).contains("rng_seed 12345")) { return 4; }

    // A different seed gives a different sequence (so seeding is real).
    var c = fuzz.fuzz_run_seeded(seeds, 300, 999 as i64, target);
    if (outcome_failed(c) && outcome_msg(c) == outcome_msg(a)) { return 5; }

    // A passing target passes at any seed.
    if (outcome_failed(fuzz.fuzz_run_seeded(seeds, 200, 7 as i64, always_pass))) { return 6; }

    // Guard rails preserved.
    if (!outcome_failed(fuzz.fuzz_run_seeded(seeds, 0, 1 as i64, always_pass))) { return 7; }
    var empty: string[] = [];
    if (!outcome_failed(fuzz.fuzz_run_seeded(empty, 10, 1 as i64, always_pass))) { return 8; }

    // The unseeded entry point still works. NOTE: this specific call is what
    // caught a self-host x86-64 miscompile when fuzz_run delegated to
    // fuzz_run_seeded -- the callee invoked a different function value than
    // the one passed, so an always-passing target reported a failure raised
    // by an earlier target. Keep this assertion.
    if (outcome_failed(fuzz.fuzz_run(seeds, 100, always_pass))) { return 9; }

    return 42;
}
`

func TestFuzzSeededInterp(t *testing.T) {
	if got := runInterpExit(t, fuzzSeededProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestFuzzSeededX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, fuzzSeededProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestFuzzSeededWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, fuzzSeededProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestFuzzSeededArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, fuzzSeededProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
