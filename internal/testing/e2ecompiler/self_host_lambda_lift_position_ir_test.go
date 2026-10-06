package e2ecompiler

import "testing"

// lambdaLiftPositionIRCases exercise no-capture lambdas in three more positions
// that the lambda-lift pre-pass now hoists to top-level `__lam_N` functions, so
// they lower through the self-host IR path on x86-64 + wasm:
//
//   - IIFE callee: `((b) => {...})(args)` -> `__lam_N(args)` (a direct call).
//   - tuple element: `((x) => {...}, 10)` -> a fn-pointer tuple element, so
//     `t.0(t.1)` takes the tuple-element call_indirect path.
//   - assignment RHS: `f = (x) => {...}` -> `f = __lam_N` (a fn-pointer store).
//
// `lift_lambdas` already hoisted no-capture lambdas in call-argument /
// array-element / struct-field / return positions; these add the IIFE-callee,
// tuple-element, and assignment-RHS positions (lift_call_arg in lift_expr_walk's
// ExprCall callee + new ExprTuple arm, and in lift_stmt's StmtAssign arm). A
// CAPTURING lambda in any of these positions is left in place (still bails to
// AST), since calling it needs the env-passing closure form.
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 126 (wasmtime exit-code truncation, cf. #2908).
var lambdaLiftPositionIRCases = []struct {
	name string
	main string
}{
	// Immediately-invoked no-capture lambda with an argument.
	{"iife", `function main(): i32 { return ((b: i32): i32 => { return b + 1; })(4); }`},
	// Two-argument IIFE.
	{"iife-2arg", `function main(): i32 { return ((a: i32, b: i32): i32 => { return a + b; })(5, 6); }`},
	// No-capture lambda as a tuple element, called via `t.0(t.1)`.
	{"tuple-fn", `function main(): i32 { let t: ((i32) => i32, i32) = ((x: i32): i32 => { return x + 1; }, 10); return t.0(t.1); }`},
	// Assigning a no-capture lambda to a fn-typed local, then calling it.
	{"reassign", `function inc(b: i32): i32 { return b + 1; }
function main(): i32 { let f: (i32) => i32 = inc; f = (x: i32): i32 => { return x * 2; }; return f(5); }`},
	// Regression: a no-capture lambda call ARGUMENT still lowers (already lifted).
	{"arg-regress", `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function main(): i32 { return apply((y: i32): i32 => { return y + 1; }, 4); }`},
}

// TestSelfHostLambdaLiftPositionIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostLambdaLiftPositionIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range lambdaLiftPositionIRCases {
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
