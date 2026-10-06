package e2ecompiler

import "testing"

// matchOrPatternIRCases pin match-arm OR-PATTERNS (`A | B => …`, issue #2698)
// to the self-host IR path on x86-64 + wasm. The parser desugars an or-pattern
// into one arm per alternative sharing the (per-alternative) guard + body, so
// the checker (exhaustiveness, payload binding) and the lowering see an ordinary
// flat arm list — no new IR. These cases prove the desugar survives the
// self-host parser end to end: payloadless variants, a same-name payload
// binding reused across alternatives, a guard applied to every alternative,
// and the expression-form match. Scope: variant patterns only (a bare `|`
// between integer literals is the bitwise-or operator on the literal-match
// path, so literal or-patterns are intentionally rejected — see the parser).
// Each result is <= 126 (wasmtime exit-code truncation, cf. #2908) and is
// oracle-checked against the reference interpreter. Mirrors
// self_host_match_guard_ir_test.go.
var matchOrPatternIRCases = []struct {
	name string
	main string
}{
	// Payloadless variant or-pattern (stmt form). pick(Red)=1, pick(Green)=2,
	// pick(Blue)=1 (via the `Red | Blue` arm). 1 + 2*10 + 1*100 = 121.
	{"variant-payloadless", `enum Color { Red, Green, Blue }
function pick(c: Color): i32 { match (c) { Red | Blue => { return 1; }, Green => { return 2; } } }
function main(): i32 { return pick(Red) + pick(Green) * 10 + pick(Blue) * 100; }`},
	// Same-name payload binding reused across both alternatives of the
	// or-pattern (the slot-allocation-prone shape). f(Sq(5))=10, f(Circ(7))=14,
	// f(Tri(3))=3. 10 + 14 + 3 = 27.
	{"variant-binding", `enum Shape { Sq(i32), Circ(i32), Tri(i32) }
function f(s: Shape): i32 { match (s) { Sq(x) | Circ(x) => { return x * 2; }, Tri(x) => { return x; } } }
function main(): i32 { return f(Sq(5)) + f(Circ(7)) + f(Tri(3)); }`},
	// A guard applied to every alternative of an or-pattern, with an unguarded
	// or-pattern fallback over the same variants (one guarded + one unguarded
	// arm per variant — the IR-eligible guard shape). pick(Has(7))=1,
	// pick(Big(2))=2, pick(Nil)=3. 1 + 2*5 + 3*25 = 86.
	{"variant-guard", `enum Opt { Has(i32), Big(i32), Nil }
function pick(o: Opt): i32 { match (o) { Has(n) | Big(n) when n > 5 => { return 1; }, Has(n) | Big(n) => { return 2; }, Nil => { return 3; } } }
function main(): i32 { return pick(Has(7)) + pick(Big(2)) * 5 + pick(Nil) * 25; }`},
	// Expression-form match with an or-pattern arm. pick(Red)=7, pick(Green)=9,
	// pick(Blue)=7. 7 + 9 + 7*2 = 30.
	{"variant-expr", `enum Color { Red, Green, Blue }
function pick(c: Color): i32 { return match (c) { Red | Blue => 7, Green => 9 }; }
function main(): i32 { return pick(Red) + pick(Green) + pick(Blue) * 2; }`},
}

// TestSelfHostMatchOrPatternIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
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
