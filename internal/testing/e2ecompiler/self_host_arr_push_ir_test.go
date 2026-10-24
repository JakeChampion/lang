package e2ecompiler

import "testing"

var arrPushIRCases = []struct {
	name string
	main string
}{
	{"len", `function main(): i32 { let a: i32[] = []; a = a.append(1); a = a.append(2); a = a.append(3); return a.len(); }`},
	{"index", `function main(): i32 { let a: i32[] = [10]; a = a.append(20); return a[1]; }`},
	{"loop-grow", `function main(): i32 { let a: i32[] = []; let i: i32 = 0; while (i < 10) { a = a.append(i * i); i = i + 1; } return a[7]; }`},
	{"loop-sum", `function main(): i32 { let a: i32[] = []; let i: i32 = 0; while (i < 5) { a = a.append(i + 1); i = i + 1; } let s: i32 = 0; for x in a { s = s + x; } return s; }`},
}

func TestSelfHostArrPushIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range arrPushIRCases {
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
