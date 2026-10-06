package e2ecompiler

import "testing"

// lambdaLiftSliceFieldIRCases finish the lambda-lift recursion coverage: a
// no-capture lambda CALL nested in a SLICE bound (`a[(iife)(0) : 3]`) or under a
// FIELD-ACCESS object (`arr[(iife)(0)].v`) is now hoisted, so the module stays on
// the IR path. These join the binary / unary / index recursion (#3148) — the
// remaining compound expression forms `lift_expr_walk` descends into.
//
// Each case is oracle-checked against the interpreter and returns a
// non-negative value <= 126 (cf. #2908).
var lambdaLiftSliceFieldIRCases = []struct {
	name string
	main string
}{
	// IIFE as a slice's start bound: a[1:3] -> len 2.
	{"slice-start", `function main(): i32 { let a: i32[] = [10, 20, 30, 40]; let s = a[((x: i32): i32 => { return x; })(1) : 3]; return s.len(); }`},
	// IIFE as a slice's end bound: a[0:3] -> len 3.
	{"slice-end", `function main(): i32 { let a: i32[] = [10, 20, 30, 40]; let s = a[0 : ((x: i32): i32 => { return x; })(3)]; return s.len(); }`},
	// IIFE as the index inside a field-access object: arr[0].v = 7.
	{"fieldaccess-index", `struct P { v: i32 }
function main(): i32 { let p = P { v: 7 }; let arr: P[] = [p]; return arr[((x: i32): i32 => { return x; })(0)].v; }`},
}

// TestSelfHostLambdaLiftSliceFieldIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostLambdaLiftSliceFieldIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range lambdaLiftSliceFieldIRCases {
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
