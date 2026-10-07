package e2ecompiler

import (
	"strconv"
	"strings"
	"testing"
)

// --- Discarded string-returning call reclaim --------------------------------
//
// `mk(a);` as a STATEMENT owns a fresh string nothing else will ever hold, so
// the discard must free it, as the same call BOUND to a local
// (`let t = mk(a);`) does.
//
// Only a callee whose every return is a freshly-allocated string qualifies. A
// callee that can hand back a borrowed arg / field / literal must be left
// alone: freeing its result drops a box a live local still holds.

// discardedStrCallSrc churns `mk(a);` n times and returns n % 7, so the exit
// code pins that the loop actually ran the requested number of rounds.
func discardedStrCallSrc(rounds int) string {
	return `function mk(s: string): string { return s + "!"; }

function churn(a: string, n: i32): i32 {
    let i: i32 = 0;
    while (i < n) { mk(a); i = i + 1; }
    return i;
}

function main(): i32 {
    let a: string = "longer_string_one_here";
    return churn(a, ` + strconv.Itoa(rounds) + `) % 7;
}`
}

// TestSelfHostDiscardedStrCallReclaimX86_64 — a discarded fresh-string call
// frees its result.
//
// The assertion is that the residue does NOT SCALE with the round count, not
// that it is zero: `main`'s own `a` is never reclaimed (a process-exit floor,
// not a leak), so a correct run still ends at allocs=601 frees=600
// live_bytes=24 for any n. Doubling n is what separates the floor from the
// leak — before the fix the same doubling took live_bytes 4824 → 9624 → 19224.
//
// frees must never EXCEED allocs either: that would mean something else had
// already claimed the value and this free is a double.
func TestSelfHostDiscardedStrCallReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	type reading struct{ allocs, frees, live int64 }
	measure := func(t *testing.T, rounds int) reading {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, discardedStrCallSrc(rounds), []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, "discarded_str_call_"+strconv.Itoa(rounds), asm)
		stderr, exit := hevRun(t, runner, progBin)
		// Confirmed against both oracles (bin/fern -interp and native -target
		// x86-64), not read off the self-host run this test exists to check.
		if want := rounds % 7; exit != want {
			t.Fatalf("rounds=%d: exited %d, want %d", rounds, exit, want)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("rounds=%d: no leakcheck summary", rounds)
		}
		var r reading
		if _, err := fmtSscan(summary, &r.allocs, &r.frees, &r.live); err != nil {
			t.Fatalf("rounds=%d: parse %q: %v", rounds, summary, err)
		}
		if r.allocs == 0 {
			t.Fatalf("rounds=%d: allocated nothing — the probe is not exercising the path", rounds)
		}
		if r.frees > r.allocs {
			t.Fatalf("rounds=%d: allocs=%d frees=%d — frees above allocs means something "+
				"else already claimed the value (double free)", rounds, r.allocs, r.frees)
		}
		return r
	}

	lo := measure(t, 200)
	hi := measure(t, 400)
	if hi.allocs <= lo.allocs {
		t.Fatalf("allocs did not grow with the round count (%d at 200, %d at 400) — "+
			"the churn is being optimised away and the measurement is meaningless",
			lo.allocs, hi.allocs)
	}
	if hi.live != lo.live {
		t.Errorf("live_bytes %d at 200 rounds, %d at 400 — a discarded fresh-string "+
			"call must free its result, so the residue is a fixed floor and doubling "+
			"the rounds must not move it", lo.live, hi.live)
	}
	if d, e := hi.allocs-hi.frees, lo.allocs-lo.frees; d != e {
		t.Errorf("unfreed allocations %d at 200 rounds, %d at 400 — must not scale "+
			"with the round count", e, d)
	}
}

// TestSelfHostDiscardedStrCallHazardsX86_64 — the discard shapes the free must
// still REFUSE. Each asserts BEHAVIOUR: a wrongly-granted free releases a box
// something else still reads, so the failure is a wrong answer or a crash, not
// a leak. Every `want` was confirmed against the interpreter and native x86-64.
func TestSelfHostDiscardedStrCallHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		// A callee returning a BORROWED value — its own param — so the discard
		// must leave it alone. Freeing it would drop the box `a` still holds;
		// the next allocation reuses it and the content check fails.
		{"borrowed_return_untouched", `function mk(s: string): string { return s + "!"; }
function pick(a: string, b: string): string { if (a.len() > 3) { return a; } return b; }
function main(): i32 {
    let a: string = mk("abcdefg");
    let b: string = "xy";
    let i: i32 = 0;
    while (i < 200) { pick(a, b); i = i + 1; }
    if (a != "abcdefg!") { return 1; }
    if (b != "xy") { return 2; }
    return 5;
}`, 5},
		// A conditional return where ONE arm is a borrowed param disqualifies
		// the whole callee: EVERY return must be fresh, including one nested
		// in an if.
		{"mixed_return_disqualifies", `function half(a: string, c: boolean): string {
    if (c) { return a; }
    return a + "!";
}
function main(): i32 {
    let a: string = "abcdefgh";
    let i: i32 = 0;
    while (i < 200) { half(a, true); i = i + 1; }
    if (a != "abcdefgh") { return 1; }
    return 6;
}`, 6},
		// The value is still correct when the same callee's result IS used
		// elsewhere in the same function — the discard's free must not reach a
		// separately-bound result.
		{"bound_and_discarded_coexist", `function mk(s: string): string { return s + "!"; }
function main(): i32 {
    let a: string = "abcdefgh";
    let keep: string = mk(a);
    let i: i32 = 0;
    while (i < 200) { mk(a); i = i + 1; }
    if (keep != "abcdefgh!") { return 1; }
    return 7;
}`, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "discard_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrongly-granted free releases a box "+
					"something else still reads", exit, tc.want)
			}
		})
	}
}
