package parser

import "testing"

// `&`, `^` and `|` bind tighter than the comparisons and looser than the
// shifts and the arithmetic, as in Rust.
func TestBitwiseBindsTighterThanComparison(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"a & b == c", "(== (& a b) c)"},
		{"a == b & c", "(== a (& b c))"},
		{"a | b != c", "(!= (| a b) c)"},
		{"a ^ b < c", "(< (^ a b) c)"},
		{"a & b | c ^ d", "(| (& a b) (^ c d))"},
		{"a + b & c", "(& (+ a b) c)"},
		{"a << 1 & b", "(& (<< a 1) b)"},
		{"a & b == c && d", "(&& (== (& a b) c) d)"},
	} {
		if got := shape(exprOfReturn(t, tc.src)); got != tc.want {
			t.Errorf("parse %q = %s, want %s", tc.src, got, tc.want)
		}
	}
}
