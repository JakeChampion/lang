package e2ecompiler

import "testing"

var whileIRCases = []struct {
	name string
	main string
}{
	{"while-sum", `function main(): i32 { let i: i32 = 1; let s: i32 = 0; while (i <= 5) { s = s + i; i = i + 1; } return s; }`},
	{"while-early-return", `function main(): i32 { let i: i32 = 0; while (i < 100) { if (i == 7) { return i; } i = i + 1; } return 0 - 1; }`},
	{"while-zero-iter", `function main(): i32 { let s: i32 = 7; let i: i32 = 5; while (i < 5) { s = s + 1; } return s; }`},
	{"while-compound-step", `function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 6) { s = s + i; i = i + 2; } return s; }`},
	{"while-nested", `function main(): i32 { let c: i32 = 0; let i: i32 = 0; while (i < 4) { let j: i32 = 0; while (j < 3) { c = c + 1; j = j + 1; } i = i + 1; } return c; }`},
}

func TestSelfHostWhileIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range whileIRCases {
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
