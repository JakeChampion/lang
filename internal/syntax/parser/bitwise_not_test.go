package parser

import "testing"

// `~` is a prefix operator beside `-` and `!`, and binds tighter than each
// binary operator.
func TestBitwiseNotPrecedence(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"~a", "(~ a)"},
		{"~~a", "(~ (~ a))"},
		{"a & ~b", "(& a (~ b))"},
		{"~a + b", "(+ (~ a) b)"},
		{"-~a", "(- (~ a))"},
	} {
		if got := shape(exprOfReturn(t, tc.src)); got != tc.want {
			t.Errorf("parse %q = %s, want %s", tc.src, got, tc.want)
		}
	}
}
