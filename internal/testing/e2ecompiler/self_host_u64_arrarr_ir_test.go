package e2ecompiler

import "testing"

var u64ArrArrIRCases = []struct {
	name string
	main string
}{
	{"nested-shr", `function main(): i32 { let m: u64[][] = [[18000000000000000000, 1], [2, 3]]; let r: u64 = m[0][0] >> 58; return r as i32; }`},
	{"nested-cmp", `function main(): i32 { let m: u64[][] = [[18000000000000000000, 1], [2, 3]]; if (m[0][0] > (100 as u64)) { return 7; } return 9; }`},
	{"nested-alias", `function main(): i32 { let m: u64[][] = [[18000000000000000000, 1], [2, 3]]; let n: u64[][] = m; let r: u64 = n[0][0] >> 58; return r as i32; }`},
	{"i64-width-regress", `function main(): i32 { let m: i64[][] = [[10, 20], [30, 40]]; return m[1][0] as i32; }`},
}

func TestSelfHostU64ArrArrIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range u64ArrArrIRCases {
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
