package e2ecompiler

import "testing"

// lambdaLiftNestedIRCases exercise no-capture lambda CALLS nested inside compound
// expressions — binary / unary / index — which the lambda-lift pre-pass reaches
// by recursing through those forms in lift_expr_walk, so a lambda call inside
// `(...) + 1`, `0 - (...)`, or `a[...]` is lifted like one in call-arg /
// array / struct-field / tuple / callee position.
//
// All lambdas here are no-capture (lifted to a top-level `__lam_N`).
// Each case is oracle-checked against the interpreter and returns a
// non-negative value <= 126 (avoiding the wasmtime exit-code truncation gap
// and the negative-exit-code ambiguity, cf. #2908).
var lambdaLiftNestedIRCases = []struct {
	name string
	main string
}{
	// IIFE in the left operand of a binary op: (5*3) + 1 = 16.
	{"iife-binary-lhs", `function main(): i32 { return ((x: i32): i32 => { return x * 3; })(5) + 1; }`},
	// IIFE in the right operand: 1 + (5*3) = 16.
	{"iife-binary-rhs", `function main(): i32 { return 1 + ((x: i32): i32 => { return x * 3; })(5); }`},
	// IIFE under a unary minus, kept positive: 100 - (5+1) = 94.
	{"iife-unary", `function main(): i32 { return 100 - ((x: i32): i32 => { return x + 1; })(5); }`},
	// IIFE as an array index: a[(1+1)] = a[2] = 30.
	{"iife-index", `function main(): i32 { let a: i32[] = [10, 20, 30]; return a[((x: i32): i32 => { return x + 1; })(1)]; }`},
	// Lambda call ARGUMENT inside a binary op: ap(\x.x+1)=4, +1 = 5.
	{"lambda-arg-binary", `function ap(f: (i32) => i32): i32 { return f(3); }
function main(): i32 { return ap((x: i32): i32 => { return x + 1; }) + 1; }`},
	// Nested deeper: a binary whose operands are both IIFE calls: 6 + 8 = 14.
	{"iife-both-operands", `function main(): i32 { return ((x: i32): i32 => { return x + 1; })(5) + ((y: i32): i32 => { return y * 2; })(4); }`},
}

// TestSelfHostLambdaLiftNestedIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostLambdaLiftNestedIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range lambdaLiftNestedIRCases {
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
