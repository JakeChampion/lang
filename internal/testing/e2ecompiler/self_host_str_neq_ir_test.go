package e2ecompiler

import "testing"

var strNeqIRCases = []struct {
	name string
	main string
}{
	{"ne-true", `function main(): i32 { let a: string = "foo"; if (a != "bar") { return 9; } return 0; }`},
	{"ne-false", `function main(): i32 { let a: string = "foo"; if (a != "foo") { return 1; } return 5; }`},
	{"ne-var", `function main(): i32 { let a: string = "abc"; let b: string = "abd"; if (a != b) { return 7; } return 0; }`},
	{"eq-ne-mix", `function main(): i32 { let a: string = "hi"; let n = 0; if (a == "hi") { n = n + 3; } if (a != "bye") { n = n + 4; } return n; }`},
}

func TestSelfHostStrNeqIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range strNeqIRCases {
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
