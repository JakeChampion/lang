package e2ecompiler

import "testing"

// fnRetIRCases exercise functions that RETURN a function value through the
// self-host IR path on x86-64 + wasm.
//
// The gap this closes: a function returning a NO-CAPTURE lambda
// (`function mk(): (i32) => i32 { return (b) => { ... }; }`) bailed to the
// AST path, even though returning a *capturing* lambda and returning a *named*
// function both already lowered. `lift_lambdas` hoisted no-capture lambdas in
// call-argument / array-element / struct-field positions but not in RETURN
// position, so the bare lambda survived to lowering and tripped the bail. The fix
// lifts the return value via lift_call_arg, turning `return (b) => {...}`
// into `return __lam_N` — the already-working named-function-return path.
//
// The capturing-return and named-return cases are included as regression guards
// (they must stay on the IR path and correct). Each case is oracle-checked
// against the interpreter and returns a value <= 126 (wasmtime exit-code
// truncation, cf. #2908).
var fnRetIRCases = []struct {
	name string
	main string
}{
	// Return a no-capture lambda, bind it, then call it: 4 + 1 = 5.
	{"nocap", `function mk(): (i32) => i32 { return (b: i32): i32 => { return b + 1; }; }
function main(): i32 { let g = mk(); return g(4); }`},
	// No-capture lambda body with multiplication: 4 * 3 = 12.
	{"nocap-mul", `function mk(): (i32) => i32 { return (b: i32): i32 => { return b * 3; }; }
function main(): i32 { let g = mk(); return g(4); }`},
	// Bound result called twice: (4+1) + (10+1) = 16.
	{"nocap-twice", `function mk(): (i32) => i32 { return (b: i32): i32 => { return b + 1; }; }
function main(): i32 { let g = mk(); return g(4) + g(10); }`},
	// Two-parameter no-capture returned lambda: 5 + 6 = 11.
	{"nocap-2arg", `function mk(): (i32, i32) => i32 { return (a: i32, b: i32): i32 => { return a + b; }; }
function main(): i32 { let g = mk(); return g(5, 6); }`},
	// Regression: returning a CAPTURING lambda still lowers (4 + 10 = 14).
	{"cap-regress", `function mk(n: i32): (i32) => i32 { return (b: i32): i32 => { return b + n; }; }
function main(): i32 { let g = mk(10); return g(4); }`},
	// Regression: returning a NAMED function still lowers (4 + 1 = 5).
	{"named-regress", `function inc(b: i32): i32 { return b + 1; }
function mk(): (i32) => i32 { return inc; }
function main(): i32 { let g = mk(); return g(4); }`},
}

// TestSelfHostFnRetIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostFnRetIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range fnRetIRCases {
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
