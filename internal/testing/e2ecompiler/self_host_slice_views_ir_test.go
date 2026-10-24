package e2ecompiler

import "testing"

var sliceViewIRCases = []struct {
	name string
	main string
}{
	{"slice-len", `function main(): i32 { let a: i32[] = [10,20,30,40]; let s: [i32] = a[1:3]; return s.len(); }`},
	{"slice-index", `function main(): i32 { let a: i32[] = [10,20,30,40]; let s: [i32] = a[1:3]; return s[0]; }`},
	{"slice-iter-sum", `function main(): i32 { let a: i32[] = [1,2,3,4,5]; let s: [i32] = a[1:4]; let t: i32 = 0; for x in s { t = t + x; } return t; }`},
	{"slice-of-slice", `function main(): i32 { let a: i32[] = [1,2,3,4,5]; let s: [i32] = a[1:5]; let s2: [i32] = s[0:2]; return s2.len(); }`},
	{"slice-as-param", `function sum(s: [i32]): i32 { let t: i32 = 0; for x in s { t = t + x; } return t; } function main(): i32 { let a: i32[] = [4,5,6,7]; return sum(a[1:3]); }`},
	{"slice-last", `function main(): i32 { let a: i32[] = [9,8,7]; let s: [i32] = a[0:3]; return s[s.len()-1]; }`},
	{"empty-slice", `function main(): i32 { let a: i32[] = [1,2,3]; let s: [i32] = a[2:2]; return s.len(); }`},
	{"string-elem-slice", `function main(): i32 { let a: string[] = ["ab","cde","f"]; let s: [string] = a[0:2]; return s[1].len(); }`},
}

func TestSelfHostSliceViewsIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range sliceViewIRCases {
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
