package e2ecompiler

import "testing"

var returnClosureIRCases = []struct {
	name string
	main string
}{
	{"curry", `function main(): i32 { let add = (x: i32) => (y: i32) => x + y; return add(3)(4); }`},
	{"return-captures-param", `function mk(k: i32): (i32) => i32 { return (y: i32) => k + y; } function main(): i32 { return mk(10)(5); }`},
	{"return-captures-local", `function mk(): (i32) => i32 { let k = 10; return (y: i32) => k + y; } function main(): i32 { return mk()(5); }`},
	{"two-arg", `function mk(k: i32): (i32, i32) => i32 { return (a: i32, b: i32) => k + a + b; } function main(): i32 { return mk(10)(5, 6); }`},
	{"two-captures", `function mk(j: i32, k: i32): (i32) => i32 { return (y: i32) => j + k + y; } function main(): i32 { return mk(10, 20)(3); }`},
	{"return-no-capture", `function mk(): (i32) => i32 { return (y: i32) => y + 1; } function main(): i32 { return mk()(41); }`},
	{"via-var", `function mk(k: i32): (i32) => i32 { return (y: i32) => k + y; } function main(): i32 { let f = mk(7); return f(8); }`},
}

func TestSelfHostReturnClosureIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range returnClosureIRCases {
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
