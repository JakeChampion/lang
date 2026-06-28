package e2e

import (
	"testing"
)

// Native-backend coverage for match or-patterns (`A | B => …`, issue #2698),
// complementing the self-host IR cases (self_host_match_orpattern_ir_test.go).
// The parser desugars an or-pattern into one arm per alternative sharing the
// guard + body, so these exercise the same enum/match lowering on x86-64,
// arm64, and wasm — payloadless variants, a same-name payload binding reused
// across alternatives, a guard over every alternative, and the expression form.
var matchOrPatternCases = []struct {
	name string
	src  string
	want int
}{
	// Payloadless or-pattern: pick(Red)=1, pick(Green)=2, pick(Blue)=1.
	// 1 + 2*10 + 1*100 = 121.
	{"payloadless", `enum Color { Red, Green, Blue }
function pick(c: Color): i32 { match (c) { Red | Blue => { return 1; }, Green => { return 2; } } }
function main(): i32 { return pick(Red) + pick(Green) * 10 + pick(Blue) * 100; }`, 121},
	// Same-name payload binding across both alternatives. f(Sq(5))=10,
	// f(Circ(7))=14, f(Tri(3))=3. 10 + 14 + 3 = 27.
	{"binding", `enum Shape { Sq(i32), Circ(i32), Tri(i32) }
function f(s: Shape): i32 { match (s) { Sq(x) | Circ(x) => { return x * 2; }, Tri(x) => { return x; } } }
function main(): i32 { return f(Sq(5)) + f(Circ(7)) + f(Tri(3)); }`, 27},
	// Guard applied to every alternative, with an unguarded or-pattern
	// fallback. pick(Has(7))=1, pick(Big(2))=2, pick(Nil)=3. 1 + 2*5 + 3*25 = 86.
	{"guard", `enum Opt { Has(i32), Big(i32), Nil }
function pick(o: Opt): i32 { match (o) { Has(n) | Big(n) when n > 5 => { return 1; }, Has(n) | Big(n) => { return 2; }, Nil => { return 3; } } }
function main(): i32 { return pick(Has(7)) + pick(Big(2)) * 5 + pick(Nil) * 25; }`, 86},
	// Expression-form match with an or-pattern arm. pick(Red)=7, pick(Green)=9,
	// pick(Blue)=7. 7 + 9 + 7*2 = 30.
	{"expr-form", `enum Color { Red, Green, Blue }
function pick(c: Color): i32 { return match (c) { Red | Blue => 7, Green => 9 }; }
function main(): i32 { return pick(Red) + pick(Green) + pick(Blue) * 2; }`, 30},
}

func TestX86_64MatchOrPattern(t *testing.T) {
	for _, c := range matchOrPatternCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

func TestArm64MatchOrPattern(t *testing.T) {
	for _, c := range matchOrPatternCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != c.want {
				t.Errorf("got %d, want %d", code, c.want)
			}
		})
	}
}

func TestWASMMatchOrPattern(t *testing.T) {
	for _, c := range matchOrPatternCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}
