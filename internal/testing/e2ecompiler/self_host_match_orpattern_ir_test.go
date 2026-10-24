package e2ecompiler

import "testing"

var matchOrPatternIRCases = []struct {
	name string
	main string
}{
	{"variant-payloadless", `enum Color { Red, Green, Blue }
function pick(c: Color): i32 { match (c) { Red | Blue => { return 1; }, Green => { return 2; } } }
function main(): i32 { return pick(Red) + pick(Green) * 10 + pick(Blue) * 100; }`},
	{"variant-binding", `enum Shape { Sq(i32), Circ(i32), Tri(i32) }
function f(s: Shape): i32 { match (s) { Sq(x) | Circ(x) => { return x * 2; }, Tri(x) => { return x; } } }
function main(): i32 { return f(Sq(5)) + f(Circ(7)) + f(Tri(3)); }`},
	{"variant-guard", `enum Opt { Has(i32), Big(i32), Nil }
function pick(o: Opt): i32 { match (o) { Has(n) | Big(n) when n > 5 => { return 1; }, Has(n) | Big(n) => { return 2; }, Nil => { return 3; } } }
function main(): i32 { return pick(Has(7)) + pick(Big(2)) * 5 + pick(Nil) * 25; }`},
	{"variant-expr", `enum Color { Red, Green, Blue }
function pick(c: Color): i32 { return match (c) { Red | Blue => 7, Green => 9 }; }
function main(): i32 { return pick(Red) + pick(Green) + pick(Blue) * 2; }`},
}

func TestSelfHostMatchOrPatternIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range matchOrPatternIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
