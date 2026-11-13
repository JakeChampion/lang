package parser

import (
	"strings"
	"testing"
)

func TestLetElseFullInitializer(t *testing.T) {
	for _, source := range []string{
		`function main(): i32 { let Some(n) = { Some(3) } else { return 1; }; return n; }`,
		`function main(): i32 { let 3 = { 3 } else { return 1; }; return 0; }`,
		`struct Box { n: i32 } function (b: Box) get(): Option[i32] { return Some(b.n); }
function main(): i32 { let Some(n) = Box { n: 3 }.get() else { return 1; }; return n; }`,
	} {
		if _, err := Parse(source); err != nil {
			t.Fatalf("initializer: %v\n%s", err, source)
		}
	}
}

func TestLetElseTupleOfPatterns(t *testing.T) {
	for _, source := range []string{
		`function f(x: Option[i32], y: Option[i32]): i32 { let (Some(a), Some(b)) = (x, y) else { return 0; }; return a + b; }`,
		`function f(t: (i32, i32)): i32 { let (1, b) = t else { return 0; }; return b; }`,
		`function f(x: Option[i32], y: Option[i32]): i32 { let w @ (Some(a), _) = (x, y) else { return 0; }; return a; }`,
		`function f(x: Option[i32], y: Option[i32]): i32 { let (Some(a), _) | (_, Some(a)) = (x, y) else { return 0; }; return a; }`,
	} {
		if _, err := Parse(source); err != nil {
			t.Fatalf("let-else tuple: %v\n%s", err, source)
		}
	}
	for source, kind := range map[string]string{
		`function f(): i32 { let (Some(a), b) = (Some(1), 2); return a + b; }`: "a variant tuple element can fail to match",
		`function f(): i32 { let (1, b) = (1, 2); return b; }`:                 "a literal tuple element can fail to match",
	} {
		_, err := Parse(source)
		if err == nil || !strings.Contains(err.Error(), kind) {
			t.Fatalf("no else: got %v, want %q\n%s", err, kind, source)
		}
	}
}
