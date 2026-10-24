package e2ecompiler

import "testing"

var u32DivRemIRCases = []struct {
	name string
	main string
}{
	{"div-highbit", `function main(): i32 { let u: u32 = 3000000000 as u32; if (u / (3 as u32) == (1000000000 as u32)) { return 5; } return 9; }`},
	{"rem-highbit", `function main(): i32 { let u: u32 = 3000000003 as u32; if (u % (10 as u32) == (3 as u32)) { return 5; } return 9; }`},
	{"div-4e9", `function main(): i32 { let u: u32 = 4000000000 as u32; if (u / (2 as u32) == (2000000000 as u32)) { return 5; } return 9; }`},
	{"div-low", `function main(): i32 { let u: u32 = 100 as u32; return (u / (4 as u32)) as i32; }`},
	{"rem-low", `function main(): i32 { let u: u32 = 100 as u32; return (u % (7 as u32)) as i32; }`},
}

func TestSelfHostU32DivRemIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range u32DivRemIRCases {
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
