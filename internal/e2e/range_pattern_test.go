// Range match-pattern coverage (#5355 slice): `match x { 1..10 => … }`.
//
// A range pattern is a scalar-match arm whose test is a bound check
// (`scr >= lo && scr <op> hi`) instead of an equality — `..` is exclusive
// of the high bound, `..=` inclusive. It rides the existing literal-match
// lowering (emitLiteralMatch / emitLiteralMatchExpr): the low bound is
// carried in MatchArm.Literal, the high in RangeHi. These tests pin the
// native x86-64 binary against the interpreter oracle across exclusive /
// inclusive ranges, the expression form, ranges mixed with plain literal
// arms, guards, and i64 scrutinees; two also run through wasm. v1 is
// signed-integer + float scrutinees (the checker rejects unsigned, which
// the interpreter oracle would compare signed).
package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
)

var rangePatternCases = []struct {
	name string
	src  string
	want int
}{
	{
		// Exclusive `..`: 5 falls in 5..10, not 1..5.
		name: "exclusive",
		src: `function cls(x: i32): i32 {
  match (x) {
    1..5 => { return 1; },
    5..10 => { return 2; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return cls(3) * 100 + cls(5) * 10 + cls(9) + cls(20); }`,
		want: 122, // 1,2,2,0
	},
	{
		// Inclusive `..=`: 5 and 10 are included.
		name: "inclusive",
		src: `function cls(x: i32): i32 {
  match (x) {
    1..=5 => { return 1; },
    6..=10 => { return 2; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return cls(5) * 100 + cls(6) * 10 + cls(10) + cls(11); }`,
		want: 122, // 1,2,2,0
	},
	{
		// Expression-form range match.
		name: "expr_form",
		src: `function cls(x: i32): i32 {
  return match (x) { 0..10 => 1, 10..20 => 2, _ => 0 };
}
function main(): i32 { return cls(5) * 100 + cls(15) * 10 + cls(25); }`,
		want: 120, // 1,2,0
	},
	{
		// A plain literal arm coexisting with a range arm — 0 is caught by
		// the exact `0` arm before the `1..10` range.
		name: "range_literal_mix",
		src: `function cls(x: i32): i32 {
  match (x) {
    0 => { return 9; },
    1..10 => { return 1; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return cls(0) * 10 + cls(5) + cls(50); }`,
		want: 91, // 9*10 + 1 + 0 (kept < 256 for the exit-code check)
	},
	{
		// A guard on a range arm; a value in range but failing the guard
		// falls to the next (unguarded) range arm.
		name: "guarded_range",
		src: `function cls(x: i32): i32 {
  match (x) {
    1..100 when x % 2 == 0 => { return 1; },
    1..100 => { return 2; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return cls(4) * 100 + cls(5) * 10 + cls(200); }`,
		want: 120, // 1,2,0
	},
	{
		// Range pattern on an i64 scrutinee.
		name: "i64_range",
		src: `function cls(x: i64): i32 {
  match (x) {
    1..100 => { return 1; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return cls(50 as i64) * 10 + cls(500 as i64); }`,
		want: 10, // 1,0
	},
}

// TestRangePatternX86_64 runs each case through the interpreter oracle and
// the native x86-64 backend, asserting agreement.
func TestRangePatternX86_64(t *testing.T) {
	for _, tc := range rangePatternCases {
		t.Run(tc.name, func(t *testing.T) {
			oracle := runInterpByte(t, tc.src)
			if oracle != tc.want {
				t.Fatalf("interp oracle = %d, want %d", oracle, tc.want)
			}
			_, code := compileAndRunX86_64(t, tc.src)
			if code != tc.want {
				t.Errorf("native x86-64 = %d, want %d (interp oracle agrees at %d)", code, tc.want, oracle)
			}
		})
	}
}

// TestRangePatternWasm runs a couple of cases through the wasm pipeline to
// confirm range lowering is backend-agnostic.
func TestRangePatternWasm(t *testing.T) {
	for _, name := range []string{"exclusive", "expr_form"} {
		tc := rangePatternCaseByName(t, name)
		t.Run(name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm = %d, want %d", got, tc.want)
			}
		})
	}
}

// Unsigned scrutinees are deferred (the interpreter compares signed); the
// checker must reject a range pattern on one with E035.
func TestRangePatternUnsignedRejected(t *testing.T) {
	src := `function cls(x: u32): i32 {
  match (x) { 1..5 => { return 1; }, _ => { return 0; } }
  return 0 - 1;
}
function main(): i32 { return 0; }`
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := checker.Check(prog); err == nil {
		t.Fatal("expected E035 for a range pattern on an unsigned scrutinee, got no error")
	}
}

func rangePatternCaseByName(t *testing.T, name string) struct {
	name string
	src  string
	want int
} {
	t.Helper()
	for _, tc := range rangePatternCases {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("no range-pattern case named %q", name)
	return rangePatternCases[0]
}
