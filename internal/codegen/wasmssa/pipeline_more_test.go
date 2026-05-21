// Additional end-to-end pipeline tests against real Lang
// source. Each test runs:
//
//	parse → check → ir.LowerWith → ssa.LiftFromIR →
//	  ssa.Optimize → wasmssa.EmitModule → wasmtime --invoke
//
// Tests SKIP gracefully on lift/emit coverage gaps so the
// Skip→Fail boundary tracks how much real Lang source the
// SSA-direct path handles. Lives in a separate file from
// pipeline_test.go so it can grow independently.

package wasmssa_test

import (
	"strconv"
	"testing"
)

// TestPipelineMin — `function min(a, b) { if (a < b) return
// a; else return b; }` — dual-return shape, distinct return
// values in each arm.
func TestPipelineMin(t *testing.T) {
	src := `
		function min(a: i32, b: i32): i32 {
			if (a < b) {
				return a;
			} else {
				return b;
			}
		}
	`
	cases := []struct {
		a, b, want int
	}{
		{5, 3, 3},
		{3, 5, 3},
		{7, 7, 7},
		{-1, 1, -1},
	}
	for _, c := range cases {
		got := compileAndRun(t, src, "min", strconv.Itoa(c.a), strconv.Itoa(c.b))
		if got != c.want {
			t.Errorf("min(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestPipelineMax — mirror of min, sanity that the same shape
// works for the opposite predicate.
func TestPipelineMax(t *testing.T) {
	src := `
		function max(a: i32, b: i32): i32 {
			if (a > b) {
				return a;
			} else {
				return b;
			}
		}
	`
	cases := []struct {
		a, b, want int
	}{
		{5, 3, 5},
		{3, 5, 5},
		{7, 7, 7},
		{-3, -7, -3},
	}
	for _, c := range cases {
		got := compileAndRun(t, src, "max", strconv.Itoa(c.a), strconv.Itoa(c.b))
		if got != c.want {
			t.Errorf("max(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestPipelineSign — `if (a < 0) return -1; else if (a > 0)
// return 1; else return 0;`. Tests nested if-else / composed
// CFG. Expected to SKIP at the EmitModule step today (the
// classifier won't recognise nested ifs); the test acts as a
// forward-looking probe so it flips to PASS once the
// classifier learns the composition.
func TestPipelineSign(t *testing.T) {
	src := `
		function sign(a: i32): i32 {
			if (a < 0) { return 0 - 1; }
			if (a > 0) { return 1; }
			return 0;
		}
	`
	cases := []struct {
		a, want int
	}{
		{-5, -1},
		{0, 0},
		{7, 1},
	}
	for _, c := range cases {
		got := compileAndRun(t, src, "sign", strconv.Itoa(c.a))
		if got != c.want {
			t.Errorf("sign(%d) = %d, want %d", c.a, got, c.want)
		}
	}
}

// TestPipelineGCD — Euclid's `gcd(a, b) = b ? gcd(b, a % b) : a`.
// Exercises self-recursion + if-else (via the same shape
// factorial uses). Expected to PASS lift+emit once OpCall is
// integrated end-to-end through the IR-lift path; today's
// IR-lift may emit shapes the classifier doesn't yet
// recognise — Skip→Fail boundary captures the gap.
func TestPipelineGCD(t *testing.T) {
	src := `
		function gcd(a: i32, b: i32): i32 {
			if (b == 0) {
				return a;
			} else {
				return gcd(b, a - (a / b) * b);
			}
		}
	`
	cases := []struct {
		a, b, want int
	}{
		{0, 5, 5},
		{12, 8, 4},
		{17, 5, 1},
		{100, 75, 25},
	}
	for _, c := range cases {
		got := compileAndRun(t, src, "gcd", strconv.Itoa(c.a), strconv.Itoa(c.b))
		if got != c.want {
			t.Errorf("gcd(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestPipelineClassify — nested early returns with an inner
// if-else that returns from BOTH arms. None of the shape-
// specific classifiers (early-return chain, dual-return,
// if-only, if-else diamond) handle this composition. It's the
// canonical "show the relooper picking up the fallback" test.
//
//	if (a < 0) return -1;
//	if (b < 0) {
//	  if (c < 0) return -2;
//	  return -3;
//	}
//	return 0;
func TestPipelineClassify(t *testing.T) {
	src := `
		function classify(a: i32, b: i32, c: i32): i32 {
			if (a < 0) { return 0 - 1; }
			if (b < 0) {
				if (c < 0) { return 0 - 2; }
				return 0 - 3;
			}
			return 0;
		}
	`
	cases := []struct {
		a, b, c, want int
	}{
		{-1, 0, 0, -1},
		{1, -1, -1, -2},
		{1, -1, 1, -3},
		{1, 1, 0, 0},
		{5, 5, 5, 0},
	}
	for _, c := range cases {
		got := compileAndRun(t, src, "classify",
			strconv.Itoa(c.a), strconv.Itoa(c.b), strconv.Itoa(c.c))
		if got != c.want {
			t.Errorf("classify(%d, %d, %d) = %d, want %d", c.a, c.b, c.c, got, c.want)
		}
	}
}
