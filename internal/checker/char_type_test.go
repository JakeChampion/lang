package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// `char` (#5629) is a distinct type from every integer. That distinctness IS
// the feature: a byte and a code point previously shared `i32`, so
// `s[i].to_upper()` (an ASCII byte fold) and `to_upper_char(cp)` (a Unicode
// mapping) had identical signatures. Every implicit conversion must be
// rejected in both directions; only an explicit cast crosses.
func TestCharRejectsImplicitConversion(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"return char as i32",
			`function f(c: char): i32 { return c; }`,
			"function returns i32 but expression is char"},
		{"return i32 as char",
			`function f(n: i32): char { return n; }`,
			"function returns char but expression is i32"},
		{"init char from int literal",
			`function main(): i32 { var c: char = 97; return 0; }`,
			"cannot assign i32 to variable of type char"},
		{"char argument to i32 param",
			`function g(n: i32): i32 { return n; }
			 function main(): i32 { var c: char = 65 as char; return g(c); }`,
			"expected i32, got char"},
		{"init u8 from char",
			`function main(): i32 { var c: char = 65 as char; var b: u8 = c; return 0; }`,
			"cannot assign char to variable of type u8"},
		{"init char from u8",
			`function main(): i32 { var b: u8 = 65; var c: char = b; return 0; }`,
			"cannot assign u8 to variable of type char"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = Check(prog)
			if err == nil {
				t.Fatalf("checked clean, want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// The explicit cast is the only crossing, and it works in both directions,
// including for a polymorphic literal (which must settle at i32 rather than
// be handed a non-numeric target).
func TestCharExplicitCasts(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"literal to char", `function main(): i32 { var c: char = 97 as char; return c as i32; }`},
		{"i32 var to char", `function main(): i32 { var n: i32 = 97; var c: char = n as char; return c as i32; }`},
		{"char to i32 inline", `function main(): i32 { var c: char = 97 as char; return (c as i32) + 1; }`},
		{"char param and return", `function f(c: char): char { return c; }
			 function main(): i32 { return f(97 as char) as i32; }`},
		{"char array element", `function main(): i32 { var a: char[] = [65 as char]; return a[0] as i32; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := Check(prog); err != nil {
				t.Fatalf("check: %v", err)
			}
		})
	}
}

// `char` is contextual, not a lexer keyword — same treatment as `str`. A
// local, a parameter, a field and a method named `char` must keep working;
// only type position is claimed.
func TestCharIsContextualNotReserved(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"local named char", `function main(): i32 { var char: i32 = 7; return char; }`},
		{"param named char", `function f(char: i32): i32 { return char; }
			 function main(): i32 { return f(7); }`},
		{"struct field named char", `struct S { char: i32 }
			 function main(): i32 { var s: S = S { char: 7 }; return s.char; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := Check(prog); err != nil {
				t.Fatalf("check: %v", err)
			}
		})
	}
}
