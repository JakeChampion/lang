package e2ecompiler

import "testing"

var u32InferredCmpIRCases = []struct {
	name string
	main string
	want int
}{
	{"u32-gt", `let a = 3000000000 as u32; let b = 5 as u32; if (a > b) { return 1; } return 0;`, 1},
	{"u32-lt", `let a = 3000000000 as u32; let b = 5 as u32; if (b < a) { return 1; } return 0;`, 1},
	{"u32-ge", `let a = 3000000000 as u32; let b = 3000000000 as u32; if (a >= b) { return 1; } return 0;`, 1},
	{"u64-gt", `let a = 18000000000000000000 as u64; let b = 5 as u64; if (a > b) { return 1; } return 0;`, 1},
}

func u32InferredCmpIRSrc(mainBody string) string {
	return "function main(): i32 { " + mainBody + " }\n"
}

func TestSelfHostU32InferredCmpIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range u32InferredCmpIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, u32InferredCmpIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
