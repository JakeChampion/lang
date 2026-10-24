package e2ecompiler

import "testing"

var i64ArrElemWidthIRCases = []struct {
	name string
	main string
}{
	{"sum-loop", `function main(): i32 { let a: i32[] = [10,20,30]; let s: i64 = 0; for i in 0..3 { s = s + a[i]; } return s as i32; }`},
	{"direct-elem", `function main(): i32 { let a: i32[] = [5,6,7]; let s: i64 = 36; return (s + a[1]) as i32; }`},
	{"neg-elems", `function main(): i32 { let a: i32[] = [-5, -3]; let s: i64 = 50; return (s + a[0] + a[1]) as i32; }`},
	{"elem-mul", `function main(): i32 { let a: i32[] = [6, 7]; let s: i64 = 0; return (s + a[0] * a[1]) as i32; }`},
	{"i64arr-keep", `function main(): i32 { let a: i64[] = [10, 32]; let s: i64 = 0; return (s + a[0] + a[1]) as i32; }`},
}

func TestSelfHostI64ArrElemWidthIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range i64ArrElemWidthIRCases {
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
