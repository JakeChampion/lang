package e2ecompiler

import "testing"

var lambdaLiftNestedIRCases = []struct {
	name string
	main string
}{
	{"iife-binary-lhs", `function main(): i32 { return ((x: i32): i32 => { return x * 3; })(5) + 1; }`},
	{"iife-binary-rhs", `function main(): i32 { return 1 + ((x: i32): i32 => { return x * 3; })(5); }`},
	{"iife-unary", `function main(): i32 { return 100 - ((x: i32): i32 => { return x + 1; })(5); }`},
	{"iife-index", `function main(): i32 { let a: i32[] = [10, 20, 30]; return a[((x: i32): i32 => { return x + 1; })(1)]; }`},
	{"lambda-arg-binary", `function ap(f: (i32) => i32): i32 { return f(3); }
function main(): i32 { return ap((x: i32): i32 => { return x + 1; }) + 1; }`},
	{"iife-both-operands", `function main(): i32 { return ((x: i32): i32 => { return x + 1; })(5) + ((y: i32): i32 => { return y * 2; })(4); }`},
}

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
