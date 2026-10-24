package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestCharRejectsImplicitConversion(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"return char as i32",
			`function f(c: char): i32 { return c; }`,
			"return value: expected i32, got char"},
		{"return i32 as char",
			`function f(n: i32): char { return n; }`,
			"return value: expected char, got i32"},
		{"init char from int literal",
			`function main(): i32 { let c: char = 97; return 0; }`,
			"expected char, got i32"},
		{"char argument to i32 param",
			`function g(n: i32): i32 { return n; }
			 function main(): i32 { let c: char = 65 as char; return g(c); }`,
			"expected i32, got char"},
		{"init u8 from char",
			`function main(): i32 { let c: char = 65 as char; let b: u8 = c; return 0; }`,
			"expected u8, got char"},
		{"init char from u8",
			`function main(): i32 { let b: u8 = 65; let c: char = b; return 0; }`,
			"expected char, got u8"},
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

func TestCharExplicitCasts(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"literal to char", `function main(): i32 { let c: char = 97 as char; return c as i32; }`},
		{"i32 let to char", `function main(): i32 { let n: i32 = 97; let c: char = n as char; return c as i32; }`},
		{"char to i32 inline", `function main(): i32 { let c: char = 97 as char; return (c as i32) + 1; }`},
		{"char param and return", `function f(c: char): char { return c; }
			 function main(): i32 { return f(97 as char) as i32; }`},
		{"char array element", `function main(): i32 { let a: char[] = [65 as char]; return a[0] as i32; }`},
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

func TestCharIsContextualNotReserved(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"local named char", `function main(): i32 { let char: i32 = 7; return char; }`},
		{"param named char", `function f(char: i32): i32 { return char; }
			 function main(): i32 { return f(7); }`},
		{"struct field named char", `struct S { char: i32 }
			 function main(): i32 { let s: S = S { char: 7 }; return s.char; }`},
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

func TestCharCastRejectsInvalidLiteral(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"above max", `function main(): i32 { let c: char = 1114112 as char; return 0; }`},
		{"far above max", `function main(): i32 { let c: char = 2147483647 as char; return 0; }`},
		{"first surrogate", `function main(): i32 { let c: char = 55296 as char; return 0; }`},
		{"last surrogate", `function main(): i32 { let c: char = 57343 as char; return 0; }`},
		{"mid surrogate", `function main(): i32 { let c: char = 56000 as char; return 0; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = Check(prog)
			if err == nil {
				t.Fatalf("expected E071, got no error")
			}
			if !hasCode(err, "E071") {
				t.Fatalf("expected E071, got: %v", err)
			}
		})
	}
}

func TestCharCastAcceptsValidLiteral(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"zero", `function main(): i32 { let c: char = 0 as char; return 0; }`},
		{"ascii", `function main(): i32 { let c: char = 65 as char; return 0; }`},
		{"just below surrogates", `function main(): i32 { let c: char = 55295 as char; return 0; }`},
		{"just above surrogates", `function main(): i32 { let c: char = 57344 as char; return 0; }`},
		{"replacement char", `function main(): i32 { let c: char = 65533 as char; return 0; }`},
		{"astral emoji", `function main(): i32 { let c: char = 128512 as char; return 0; }`},
		{"max scalar", `function main(): i32 { let c: char = 1114111 as char; return 0; }`},
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

func TestCharAndByteLiteralsDoNotInterconvert(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"i32 let from char literal",
			`function main(): i32 { let n: i32 = 'x'; return n; }`,
			"expected i32, got char"},
		{"char let from byte literal",
			`function main(): i32 { let c: char = b'x'; return 0; }`,
			"expected char, got u8"},
		{"u8 let from char literal",
			`function main(): i32 { let b: u8 = 'x'; return 0; }`,
			"expected u8, got char"},
		{"char literal compared with i32 literal",
			`function main(): i32 { if ('x' == 120) { return 1; } return 0; }`,
			"cannot compare char with i32"},
		{"string index compared with char literal",
			`function main(): i32 { let s: string = "ab"; if (s[0] == 'a') { return 1; } return 0; }`,
			"cannot compare u8 with char"},
		{"char literal compared with byte literal",
			`function main(): i32 { if ('x' == b'x') { return 1; } return 0; }`,
			"cannot compare char with u8"},
		{"arithmetic on a char literal",
			`function main(): i32 { return 'x' + 1; }`,
			`operator "+" requires an integer type, got char`},
		{"char literal returned as i32",
			`function f(): i32 { return 'x'; }
			 function main(): i32 { return f(); }`,
			"return value: expected i32, got char"},
		{"char literal in a u8 match arm",
			`function main(): i32 { let s: string = "ab"; match (s[0]) { 'a' => { return 1; }, _ => { return 0; } } return 0; }`,
			"literal pattern of type char does not match scrutinee type u8"},
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

func TestCharAndByteLiteralsAccepted(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"byte literal against a string index",
			`function main(): i32 { let s: string = "a[b]"; if (s[1] == b'[') { return 1; } return 0; }`},
		{"byte literal against a str view index",
			`function main(): i32 { let s: string = "a[b]"; let v: str = slice_unchecked(s, 0, 2); if (v[1] == b'[') { return 1; } return 0; }`},
		{"byte literal in a u8 let and match",
			`function main(): i32 { let b: u8 = b'\n'; match (b) { b'\n' => { return 1; }, _ => { return 0; } } return 0; }`},
		{"char literal in a char let and match",
			`function main(): i32 { let c: char = 'x'; match (c) { 'x' => { return 1; }, _ => { return 0; } } return 0; }`},
		{"char literal cast to i32",
			`function main(): i32 { return 'x' as i32; }`},
		{"byte literal cast to i32",
			`function main(): i32 { return b'x' as i32; }`},
		{"char literal at a char parameter",
			`function f(c: char): char { return c; }
			 function main(): i32 { return f('x') as i32; }`},
		{"astral scalar in a char array",
			`function main(): i32 { let a: char[] = ['x', '\u{1F600}']; return a[1] as i32; }`},
		{"byte literal in a u8 array",
			`function main(): i32 { let a: u8[] = [b'a', b'b']; return a[0] as i32; }`},
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

func TestCharCastFromRuntimeValueUnchecked(t *testing.T) {
	src := `function main(): i32 { let n: i32 = 1114112; let c: char = n as char; return 0; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("a runtime cast must stay unchecked, got: %v", err)
	}
}
