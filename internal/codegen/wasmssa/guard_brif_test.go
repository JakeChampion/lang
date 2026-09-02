package wasmssa_test

import (
	"strconv"
	"testing"
)

// A rotated loop tests its condition at the BOTTOM and guards entry with a
// copy of it at the top, so a zero-trip loop is skipped by a branch that
// leaves the loop from OUTSIDE it. That branch's target (the loop's exit) and
// its fall-through (the loop header) both looked like fall-throughs to the
// relooper, which then discarded the condition and ran the body regardless.
//
// Every case here has a loop that must run ZERO times with something
// observable after it, since a loop that is the last thing in a function
// cannot show the defect: its exit has no block to land on.

func TestZeroTripLoopGuardIsNotDropped(t *testing.T) {
	src := `
		function f(n: i32): i32 {
			var s: i32 = 0;
			while (s < n) { s = s + 100; }
			return s + 1;
		}
	`
	for _, c := range []struct{ n, want int }{
		{0, 1},   // guard false at entry: body must not run
		{1, 101}, // one trip
		{250, 301},
	} {
		if got := compileAndRun(t, src, "f", strconv.Itoa(c.n)); got != c.want {
			t.Errorf("f(%d) = %d, want %d", c.n, got, c.want)
		}
	}
}

// The same shape for `for`, whose guard sits before the step.
func TestZeroTripForGuardIsNotDropped(t *testing.T) {
	src := `
		function f(n: i32): i32 {
			var s: i32 = 0;
			for (var i: i32 = 0; i < n; i = i + 1) { s = s + 10; }
			return s + 1;
		}
	`
	for _, c := range []struct{ n, want int }{
		{0, 1},
		{3, 31},
	} {
		if got := compileAndRun(t, src, "f", strconv.Itoa(c.n)); got != c.want {
			t.Errorf("f(%d) = %d, want %d", c.n, got, c.want)
		}
	}
}

// `break` reaches the same exit block, and a loop AFTER it puts a second
// guard in the position that fails. Kept as its own case because the shape —
// an early exit feeding a later loop — is how the defect shows up in real
// code rather than in a minimal repro.
func TestBreakThenSecondLoop(t *testing.T) {
	src := `
		function f(n: i32): i32 {
			var s: i32 = 0;
			var i: i32 = 0;
			while (i < 10) {
				if (i == n) { break; }
				s = s + 1;
				i = i + 1;
			}
			var j: i32 = 0;
			while (j < n) { s = s + 10; j = j + 1; }
			return s * 2 + 1;
		}
	`
	for _, c := range []struct{ n, want int }{
		{0, 1},  // both loops zero-trip
		{2, 45}, // 2 + 20
		{4, 89}, // 4 + 40
	} {
		if got := compileAndRun(t, src, "f", strconv.Itoa(c.n)); got != c.want {
			t.Errorf("f(%d) = %d, want %d", c.n, got, c.want)
		}
	}
}

// The unconditional `br` arm of the same reasoning. No source shape reaching
// it has been found — every `break` measured here goes through `br_if` — so
// this exercises the path without pinning the bug it would be. It is fixed
// alongside because the unsound predicate was shared, not duplicated.
func TestBreakToLoopExitIsNotDroppedWhenCodeFollows(t *testing.T) {
	src := `
		function f(n: i32): i32 {
			var s: i32 = 0;
			var i: i32 = 0;
			while (i < 100) {
				if (i == n) { break; }
				s = s + 1;
				i = i + 1;
			}
			return s * 2 + 1;
		}
	`
	for _, c := range []struct{ n, want int }{
		{0, 1},     // break on the first iteration
		{5, 11},    // five trips, then break
		{200, 201}, // never equal: the loop runs to its own bound
	} {
		if got := compileAndRun(t, src, "f", strconv.Itoa(c.n)); got != c.want {
			t.Errorf("f(%d) = %d, want %d", c.n, got, c.want)
		}
	}
}
