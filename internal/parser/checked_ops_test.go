package parser

import "testing"

// The checked operators `+?` / `-?` / `*?` (#5542) sit in the same arithmetic
// tiers as their wrapping counterparts: `+?` / `-?` bind like `+` / `-`, `*?`
// binds like `*`. They lex as single two-character punctuators, so the trailing
// `?` never merges with the postfix Option-try `?` (which only follows a
// completed operand, never an arithmetic operator).
func TestCheckedOperatorPrecedence(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"a +? b", "(+? a b)"},
		{"a -? b", "(-? a b)"},
		{"a *? b", "(*? a b)"},
		{"a /? b", "(/? a b)"},
		{"a %? b", "(%? a b)"},
		// `*` binds tighter than `+?`.
		{"a +? b * c", "(+? a (* b c))"},
		// `*?` binds tighter than `+`.
		{"a + b *? c", "(+ a (*? b c))"},
		// `/?` / `%?` bind in the multiplicative tier, like `/` / `%`.
		{"a + b /? c", "(+ a (/? b c))"},
		{"a /? b + c", "(+ (/? a b) c)"},
		// Same tier as the wrapping additive ops, left-associative.
		{"a + b -? c", "(-? (+ a b) c)"},
		{"a *? b / c", "(/ (*? a b) c)"},
		// Shifts are looser than the additive tier.
		{"a << b +? c", "(<< a (+? b c))"},
	} {
		if got := shape(exprOfReturn(t, tc.src)); got != tc.want {
			t.Errorf("parse %q = %s, want %s", tc.src, got, tc.want)
		}
	}
}

// TestPostfixTryStillLexesBesideChecked pins that adding `+?` / `-?` / `*?` to
// the multi-character punctuator table did not shadow the postfix Option-try
// `?` or the compound assignments.
func TestPostfixTryStillLexesBesideChecked(t *testing.T) {
	for _, src := range []string{
		`function f(): Option[i32] { var a: Option[i32] = Some(1); var b: i32 = a?; return Some(b); }`,
		`function main(): i32 { var a: i32 = 1; a += 2; return a; }`,
		`function main(): i32 { var a: i32 = 1; var b: i32 = 2; return a * b; }`,
	} {
		if _, err := Parse(src); err != nil {
			t.Errorf("parse %q: %v", src, err)
		}
	}
}
