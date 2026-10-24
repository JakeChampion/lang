package e2ecompiler

import "testing"

var lambdaLiftPositionIRCases = []struct {
	name string
	main string
}{
	{"iife", `function main(): i32 { return ((b: i32): i32 => { return b + 1; })(4); }`},
	{"iife-2arg", `function main(): i32 { return ((a: i32, b: i32): i32 => { return a + b; })(5, 6); }`},
	{"tuple-fn", `function main(): i32 { let t: ((i32) => i32, i32) = ((x: i32): i32 => { return x + 1; }, 10); return t.0(t.1); }`},
	{"reassign", `function inc(b: i32): i32 { return b + 1; }
function main(): i32 { let f: (i32) => i32 = inc; f = (x: i32): i32 => { return x * 2; }; return f(5); }`},
	{"arg-regress", `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function main(): i32 { return apply((y: i32): i32 => { return y + 1; }, 4); }`},
}

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
