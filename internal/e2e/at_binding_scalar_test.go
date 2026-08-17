// `@` bindings over literal and range sub-patterns (#2698). The `@` parse
// used to accept only variant, struct and tuple sub-patterns, so `n @ 1..10`
// — the form the unified pattern grammar is specified around — was rejected.
//
// The cases below are mostly about the payoff of sharing one grammar rather
// than about the scalar arm itself: `if let` and `let … else` read the same
// parseMatchPattern, so widening it there reaches every binding site at once
// with no per-site work. `_` stays the one sub-pattern an `@` cannot carry.
package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/modload"
)

var atBindingScalarCases = []struct {
	name string
	src  string
	want int
}{
	{
		// Range, plain literal, and a guard that names the `@` binding.
		name: "match_stmt",
		src: `function classify(n: i32): i32 {
  match (n) {
    k @ 1..10 => { return k * 2; },
    k @ 20 => { return k + 1; },
    k @ 30..=31 when k > 30 => { return k; },
    _ => { return 0; },
  }
}
function main(): i32 {
  return classify(5) + classify(20) + classify(31) + classify(30) + classify(99);
}`,
		want: 62, // 10 + 21 + 31 + 0 (guard fails) + 0
	},
	{
		// Expression form takes the same arms.
		name: "match_expr",
		src: `function pick(n: i32): i32 {
  return match (n) { k @ 5..7 => k * 3, k @ 40 => k, _ => 7 };
}
function main(): i32 { return pick(6) + pick(40) + pick(99); }`,
		want: 65, // 18 + 40 + 7
	},
	{
		// The point of one grammar: `if let` gained this for free.
		name: "if_let",
		src: `function a(n: i32): i32 { if let k @ 1..10 = n { return k * 2; } return 0; }
function main(): i32 { return a(5) + a(99); }`,
		want: 10,
	},
	{
		// And so did `let … else`.
		name: "let_else",
		src: `function b(n: i32): i32 { let k @ 20..30 = n else { return 0; }; return k + 1; }
function main(): i32 { return b(25) + b(99); }`,
		want: 26,
	},
	{
		// A negative bound is a literal like any other, and the sign has to
		// survive into the comparison.
		name: "negative_bounds",
		src: `function c(n: i32): i32 {
  match (n) { k @ -10..0 => { return 0 - k; }, k @ -20 => { return k; }, _ => { return 0; } }
}
function main(): i32 { return c(-5) + c(-20) + c(50) + 20; }`,
		want: 5, // 5 + (-20) + 0 + 20
	},
	{
		// A string scrutinee reaches the same arm shape.
		name: "string_scrutinee",
		src: `function d(s: string): i32 {
  match (s) { k @ "yes" => { return k.len(); }, _ => { return 0; } }
}
function main(): i32 { return d("yes") + d("no"); }`,
		want: 3,
	},
}

func TestAtBindingScalarInterp(t *testing.T) {
	for _, tc := range atBindingScalarCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runInterpByte(t, tc.src); got != tc.want {
				t.Errorf("interp = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAtBindingScalarX86_64(t *testing.T) {
	for _, tc := range atBindingScalarCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, tc.src); code != tc.want {
				t.Errorf("native x86-64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestAtBindingScalarArm64(t *testing.T) {
	for _, tc := range atBindingScalarCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, tc.src); code != tc.want {
				t.Errorf("native arm64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestAtBindingScalarWasm(t *testing.T) {
	for _, tc := range atBindingScalarCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm = %d, want %d", got, tc.want)
			}
		})
	}
}

// `_` binds nothing to project and is the unconditional default at every
// downstream stage, so it is the one sub-pattern an `@` cannot carry.
func TestAtBindingWildcardRejected(t *testing.T) {
	src := `function main(): i32 { match (1) { k @ _ => { return k; } } }`
	_, _, err := modload.LoadSource(src)
	if err == nil {
		t.Fatalf("`k @ _` was accepted, want a diagnostic")
	}
	if !strings.Contains(err.Error(), "`_` pattern") {
		t.Errorf("diagnostic = %v, want it to name the `_` pattern", err)
	}
}
