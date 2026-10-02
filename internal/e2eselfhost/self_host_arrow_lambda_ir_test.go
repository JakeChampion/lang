package e2eselfhost

import "testing"

// arrowLambdaIRCases pin the self-host IR lowering of the arrow-lambda spelling
// `(params): ret => expr` — specifically the CAPTURING-closure shapes through the
// self-hosted compiler's IR path. The self-host parser desugars an arrow lambda
// (arrow_lambda_at lookahead → parse_arrow_lambda, see parser.fern / #2701) to the
// SAME ExprLambda the verbose `(params): ret => { body }` form produces (an
// expression body becomes `[return expr]`), so the existing lambda-lift +
// closure-box machinery (lift_lambdas / closure_lift_one) lowers it unchanged.
//
// This complements internal/e2e/arrow_lambda_test.go, which exercises arrow
// lambdas only through the NATIVE Go backends with NON-capturing lambdas; here
// the capturing cases cover the closure-lift path the native test never
// touches. Each case is oracle-checked against the interpreter and returns a
// value <= 120 (cf. the wasmtime exit-code gap #2908).
var arrowLambdaIRCases = []struct {
	name string
	main string
}{
	// Capture-free arrow lambda, bound and called.
	{"noncap", `function main(): i32 { let f = (x: i32): i32 => x + 1; return f(5); }`},
	// Capturing an outer scalar.
	{"capture", `function main(): i32 { let n = 10; let f = (x: i32): i32 => x + n; return f(5); }`},
	// Zero-arg capturing arrow lambda.
	{"capture-noargs", `function main(): i32 { let n = 7; let f = (): i32 => n * 2; return f(); }`},
	// Two params + a capture.
	{"two-params-cap", `function main(): i32 { let k = 3; let f = (a: i32, b: i32): i32 => a + b + k; return f(4, 5); }`},
	// Capture used twice in the body expression.
	{"capture-twice", `function main(): i32 { let n = 6; let f = (x: i32): i32 => x * n + n; return f(4); }`},
	// Regression: the () => {} closure form still lowers.
	{"fn-form-regress", `function main(): i32 { let n = 10; let f = (x: i32): i32 => { return x + n; }; return f(5); }`},
	// `own` on the FIRST parameter: the lookahead must read past the modifier
	// to the `name: T` shape, as it does on a declaration.
	{"own-first-param", `function main(): i32 { let f = (own a: string[]): string[] => a; let xs: string[] = f(["x", "y"]); return xs.len(); }`},
}

// TestSelfHostArrowLambdaIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostArrowLambdaIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range arrowLambdaIRCases {
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
