package e2e

import (
	"testing"
)

var matchOrPatternCases = []struct {
	name string
	src  string
	want int
}{
	{"payloadless", `enum Color { Red, Green, Blue }
function pick(c: Color): i32 { match (c) { Red | Blue => { return 1; }, Green => { return 2; } } }
function main(): i32 { return pick(Red) + pick(Green) * 10 + pick(Blue) * 100; }`, 121},
	{"binding", `enum Shape { Sq(i32), Circ(i32), Tri(i32) }
function f(s: Shape): i32 { match (s) { Sq(x) | Circ(x) => { return x * 2; }, Tri(x) => { return x; } } }
function main(): i32 { return f(Sq(5)) + f(Circ(7)) + f(Tri(3)); }`, 27},
	{"guard", `enum Opt { Has(i32), Big(i32), Nil }
function pick(o: Opt): i32 { match (o) { Has(n) | Big(n) when n > 5 => { return 1; }, Has(n) | Big(n) => { return 2; }, Nil => { return 3; } } }
function main(): i32 { return pick(Has(7)) + pick(Big(2)) * 5 + pick(Nil) * 25; }`, 86},
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
