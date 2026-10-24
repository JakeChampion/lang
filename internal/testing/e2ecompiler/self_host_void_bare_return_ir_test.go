package e2ecompiler

import "testing"

var voidBareReturnIRCases = []struct {
	name string
	main string
}{
	{"tail", `function f(x: i32): void { print("hi"); return; } function main(): i32 { f(5); return 42; }`},
	{"early-taken", `function f(x: i32): void { if (x > 0) { return; } print("neg"); } function main(): i32 { f(5); return 42; }`},
	{"early-not-taken", `function f(x: i32): void { if (x > 0) { return; } print("neg"); } function main(): i32 { f(0 - 1); return 42; }`},
	{"in-loop", `function f(n: i32): void { let i: i32 = 0; while (i < n) { if (i == 2) { return; } print("x"); i = i + 1; } } function main(): i32 { f(5); return 42; }`},
	{"two-helpers", `function a(x: i32): void { if (x > 0) { return; } print("a"); } function b(x: i32): void { print("b"); return; } function main(): i32 { a(1); b(2); return 42; }`},
}

func TestSelfHostVoidBareReturnIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range voidBareReturnIRCases {
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
