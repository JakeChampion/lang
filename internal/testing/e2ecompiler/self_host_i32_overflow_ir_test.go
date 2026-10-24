package e2ecompiler

import "testing"

var i32OverflowIRCases = []struct {
	name string
	main string
}{
	{"add", `function main(): i32 { let x = 2147483647; let y = x + 1; if (y < 0) { return 1; } return 0; }`},
	{"mul", `function main(): i32 { let x = 65536; let y = x * x; if (y == 0) { return 5; } return 0; }`},
	{"shl", `function main(): i32 { let x = 1; let y = x << 31; if (y < 0) { return 3; } return 0; }`},
	{"sub", `function main(): i32 { let x = 0 - 2000000000; let y = x - 2000000000; if (y > 0) { return 7; } return 0; }`},
}

func TestSelfHostI32OverflowIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range i32OverflowIRCases {
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
