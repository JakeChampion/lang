// Negative-i32 slice-bound contract (#5294): an i32 constant materialises
// zero-extended (x86-64 `mov eax, N`, arm64 `mov w0, N`), so a NEGATIVE
// constant reaching 64-bit slice-bound arithmetic carried dirty high bits —
// `s.len() + (0 - 2)` computed 0x1_0000_0003 instead of 3, slipping past
// __str_slice's (partly-unsigned) bounds compares and reading out of bounds
// (an abort at driver scale; the additive-fold experiment in #5286 surfaced
// it via `len - 1 - 1` → `len + (-2)` in the self-host modload driver).
// __str_slice now sign-extends its i32 low/high bounds from the low 32 bits
// (movsxd / sxtw) before comparing — a no-op for clean bounds, so correct
// slices are unchanged.
package e2e

import "testing"

func TestNegConstSliceBound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping neg-const slice-bound e2e in -short mode")
	}
	// The reduced #5294 repro: len(“hello”) + (-2) = 3, slice [0:4] → "hell"
	// → 4. With zero-extended bounds this trapped (exit 134) on x86-64 and
	// arm64 while the interpreter returned 4.
	assertBackendsAgreeWithInterp(t, "neg_const_bound",
		`function f(s: string): i32 { var i: i32 = s.len() + (0 - 2); var sub: str = s[0 : i + 1]; return sub.len(); }
function main(): i32 { return f("hello"); }`)
	// A folded-chain shape (`len - 1 - 1`), the form the modload driver hits.
	assertBackendsAgreeWithInterp(t, "neg_const_chain",
		`function f(s: string): i32 { var i: i32 = s.len() - 1 - 1; var sub: str = s[0 : i + 1]; return sub.len(); }
function main(): i32 { return f("hello"); }`)
	// Regression: ordinary clean-bound slices are unchanged by the
	// sign-extension (movsxd/sxtw is a no-op on clean values). 18.
	assertBackendsAgreeWithInterp(t, "clean_bounds_regress",
		`function main(): i32 { var s: string = "hello world"; var a: str = s[0:5]; var b: str = s[6:11]; var c: str = s[2:10]; return a.len() + b.len() + c.len(); }`)
}
