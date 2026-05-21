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

// TestPipelineIfThenWhile — an if-else preceding a while loop.
// Existing while-loop classifier rejects (it requires exactly
// 4 blocks, no preceding if/else); the relooper picks it up.
//
//	function f(p, n) {
//	  var bonus = p ? 100 : 0;
//	  var i = 0;
//	  var total = 0;
//	  while (i < n) { total = total + i; i = i + 1; }
//	  return total + bonus;
//	}
func TestPipelineIfThenWhile(t *testing.T) {
	src := `
		function f(p: i32, n: i32): i32 {
			var bonus: i32 = 0;
			if (p != 0) { bonus = 100; }
			var i: i32 = 0;
			var total: i32 = 0;
			while (i < n) {
				total = total + i;
				i = i + 1;
			}
			return total + bonus;
		}
	`
	cases := []struct {
		p, n, want int
	}{
		{1, 5, 110}, // 100 + (0+1+2+3+4)
		{0, 5, 10},  // 0 + 10
		{1, 0, 100}, // 100 + 0
		{0, 10, 45}, // 0 + (0..9)
	}
	for _, c := range cases {
		got := compileAndRun(t, src, "f", strconv.Itoa(c.p), strconv.Itoa(c.n))
		if got != c.want {
			t.Errorf("f(%d, %d) = %d, want %d", c.p, c.n, got, c.want)
		}
	}
}

// TestPipelineSequentialWhiles — two while loops in series.
// The single-loop relooper rejected; the multi-loop relooper
// picks it up.
func TestPipelineSequentialWhiles(t *testing.T) {
	src := `
		function f(n: i32): i32 {
			var s: i32 = 0;
			var i: i32 = 0;
			while (i < n) {
				s = s + i;
				i = i + 1;
			}
			var j: i32 = 0;
			while (j < n) {
				s = s + 1;
				j = j + 1;
			}
			return s;
		}
	`
	cases := []struct {
		n, want int
	}{
		{0, 0},
		{3, 6},  // (0+1+2) + 3
		{5, 15}, // 10 + 5
	}
	for _, c := range cases {
		got := compileAndRun(t, src, "f", strconv.Itoa(c.n))
		if got != c.want {
			t.Errorf("f(%d) = %d, want %d", c.n, got, c.want)
		}
	}
}

// TestPipelineNestedWhiles — outer loop with inner loop in
// the body; verifies the multi-loop relooper handles nested
// loops lifted from real Lang source.
func TestPipelineNestedWhiles(t *testing.T) {
	src := `
		function mul(m: i32, n: i32): i32 {
			var s: i32 = 0;
			var i: i32 = 0;
			while (i < m) {
				var j: i32 = 0;
				while (j < n) {
					s = s + 1;
					j = j + 1;
				}
				i = i + 1;
			}
			return s;
		}
	`
	cases := []struct {
		m, n, want int
	}{
		{0, 5, 0},
		{3, 0, 0},
		{3, 4, 12},
		{5, 5, 25},
		{7, 11, 77},
	}
	for _, c := range cases {
		got := compileAndRun(t, src, "mul", strconv.Itoa(c.m), strconv.Itoa(c.n))
		if got != c.want {
			t.Errorf("mul(%d, %d) = %d, want %d", c.m, c.n, got, c.want)
		}
	}
}

// TestPipelineI64Sum — i64 sum loop from real Lang source.
// Verifies the lift propagates Width=64 from ir.OpConstI64 and
// the binary arith ops, the wasmssa backend emits i64.add etc.,
// and the function type signature uses (i64) → i64.
func TestPipelineI64Sum(t *testing.T) {
	src := `
		function f(n: i64): i64 {
			var s: i64 = 0;
			var i: i64 = 0;
			while (i < n) {
				s = s + i;
				i = i + 1;
			}
			return s;
		}
	`
	cases := []struct {
		n, want int64
	}{
		{0, 0},
		{10, 45},
		{1_000, 499_500},
		{100_000, 100_000 * 99_999 / 2},
	}
	for _, c := range cases {
		got := compileAndRun(t, src, "f", strconv.FormatInt(c.n, 10))
		if int64(got) != c.want {
			t.Errorf("i64 sum(%d) = %d, want %d", c.n, got, c.want)
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
