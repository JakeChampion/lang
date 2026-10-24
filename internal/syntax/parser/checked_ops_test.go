package parser

import "testing"

func TestCheckedOperatorPrecedence(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"a +? b", "(+? a b)"},
		{"a -? b", "(-? a b)"},
		{"a *? b", "(*? a b)"},
		{"a /? b", "(/? a b)"},
		{"a %? b", "(%? a b)"},
		{"a +? b * c", "(+? a (* b c))"},
		{"a + b *? c", "(+ a (*? b c))"},
		{"a + b /? c", "(+ a (/? b c))"},
		{"a /? b + c", "(+ (/? a b) c)"},
		{"a <<? b", "(<<? a b)"},
		{"a >>? b", "(>>? a b)"},
		{"a + b <<? c", "(<<? (+ a b) c)"},
		{"a <<? b + c", "(<<? a (+ b c))"},
		{"a + b -? c", "(-? (+ a b) c)"},
		{"a *? b / c", "(/ (*? a b) c)"},
		{"a << b +? c", "(<< a (+? b c))"},
	} {
		if got := shape(exprOfReturn(t, tc.src)); got != tc.want {
			t.Errorf("parse %q = %s, want %s", tc.src, got, tc.want)
		}
	}
}

func TestPostfixTryStillLexesBesideChecked(t *testing.T) {
	for _, src := range []string{
		`function f(): Option[i32] { let a: Option[i32] = Some(1); let b: i32 = a?; return Some(b); }`,
		`function main(): i32 { let a: i32 = 1; a += 2; return a; }`,
		`function main(): i32 { let a: i32 = 1; let b: i32 = 2; return a * b; }`,
	} {
		if _, err := Parse(src); err != nil {
			t.Errorf("parse %q: %v", src, err)
		}
	}
}
