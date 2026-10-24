package e2ecompiler

import "testing"

var u64TupArrIRCases = []struct {
	name string
	main string
}{
	{"index-field-shr", `function main(): i32 { let xs: (u64, i32)[] = [(18000000000000000000 as u64, 1)]; return (xs[0].0 >> 58) as i32; }`},
	{"index-field-shr-bare", `function main(): i32 { let xs: (u64, i32)[] = [(18000000000000000000, 1)]; return (xs[0].0 >> 58) as i32; }`},
	{"second-elem-shr", `function main(): i32 { let xs: (i32, u64)[] = [(1, 18000000000000000000 as u64)]; return (xs[0].1 >> 58) as i32; }`},
	{"index-field-cmp", `function main(): i32 { let xs: (u64, i32)[] = [(18000000000000000000 as u64, 1)]; if (xs[0].0 > (100 as u64)) { return 7; } return 9; }`},
	{"bound-elem-shr", `function main(): i32 { let xs: (u64, i32)[] = [(18000000000000000000 as u64, 1)]; let t = xs[0]; return (t.0 >> 58) as i32; }`},
	{"i64-elem-width-regress", `function main(): i32 { let xs: (i64, i32)[] = [(5000000007, 1)]; return (xs[0].0 % 1000) as i32; }`},
}

func TestSelfHostU64TupArrIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range u64TupArrIRCases {
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
