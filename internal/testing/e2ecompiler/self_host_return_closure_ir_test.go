package e2ecompiler

import "testing"

// returnClosureIRCases exercise calling a capturing closure that is RETURNED
// from a function, directly off the call result (`mk(..)(args)`) — the inline
// call-on-call shape. Handling only a callee that returns a bare fn pointer
// (no-capture lambda) and bailing when the callee returns a CLOSURE drops the
// module to the AST path, because that lowering
// box (a capturing-lambda-returning fn). The fix dispatches env-first off the
// returned box, the same shape `let f = mk(..); f(args)` already used.
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 120 (wasmtime exit-code truncation, cf. #2908).
var returnClosureIRCases = []struct {
	name string
	main string
}{
	// A curried closure: `(x) => (y) => x + y` — the inner lambda captures x.
	{"curry", `function main(): i32 { let add = (x: i32) => (y: i32) => x + y; return add(3)(4); }`},
	// A named function returning a closure that captures its PARAMETER.
	{"return-captures-param", `function mk(k: i32): (i32) => i32 { return (y: i32) => k + y; } function main(): i32 { return mk(10)(5); }`},
	// ...capturing a LOCAL declared in the outer function.
	{"return-captures-local", `function mk(): (i32) => i32 { let k = 10; return (y: i32) => k + y; } function main(): i32 { return mk()(5); }`},
	// The returned closure takes TWO args (call_indirect arity = 2 + env).
	{"two-arg", `function mk(k: i32): (i32, i32) => i32 { return (a: i32, b: i32) => k + a + b; } function main(): i32 { return mk(10)(5, 6); }`},
	// Two captures (env box [funcval, j, k]).
	{"two-captures", `function mk(j: i32, k: i32): (i32) => i32 { return (y: i32) => j + k + y; } function main(): i32 { return mk(10, 20)(3); }`},
	// Regression: returning a NO-capture lambda (bare fn pointer) still works.
	{"return-no-capture", `function mk(): (i32) => i32 { return (y: i32) => y + 1; } function main(): i32 { return mk()(41); }`},
	// Regression: the via-var form (already worked) stays correct.
	{"via-var", `function mk(k: i32): (i32) => i32 { return (y: i32) => k + y; } function main(): i32 { let f = mk(7); return f(8); }`},
}

// TestSelfHostReturnClosureIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostReturnClosureIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range returnClosureIRCases {
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
