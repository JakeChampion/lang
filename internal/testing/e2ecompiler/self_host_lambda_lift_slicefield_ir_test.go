package e2ecompiler

import "testing"

var lambdaLiftSliceFieldIRCases = []struct {
	name string
	main string
}{
	{"slice-start", `function main(): i32 { let a: i32[] = [10, 20, 30, 40]; let s = a[((x: i32): i32 => { return x; })(1) : 3]; return s.len(); }`},
	{"slice-end", `function main(): i32 { let a: i32[] = [10, 20, 30, 40]; let s = a[0 : ((x: i32): i32 => { return x; })(3)]; return s.len(); }`},
	{"fieldaccess-index", `struct P { v: i32 }
function main(): i32 { let p = P { v: 7 }; let arr: P[] = [p]; return arr[((x: i32): i32 => { return x; })(0)].v; }`},
}

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
