package e2ecompiler

import "testing"

var i64CallWidthIRCases = []struct {
	name string
	main string
}{
	{"call-free", `function g(): i32 { return 12; } function main(): i32 { let s: i64 = 30; return (s + g()) as i32; }`},
	{"call-arg", `function g(x: i32): i32 { return x * 2; } function main(): i32 { let s: i64 = 30; return (s + g(6)) as i32; }`},
	{"call-neg", `function g(): i32 { return -8; } function main(): i32 { let s: i64 = 50; return (s + g()) as i32; }`},
	{"call-method", `struct C { n: i32 } function (c: C) val(): i32 { return c.n; } function main(): i32 { let c: C = C { n: 12 }; let s: i64 = 30; return (s + c.val()) as i32; }`},
	{"call-loop", `function inc(x: i32): i32 { return x + 1; } function main(): i32 { let s: i64 = 0; for i in 0..3 { s = s + inc(i); } return s as i32; }`},
	{"call-i64-keep", `function g(): i64 { return 42; } function main(): i32 { let s: i64 = 0; return (s + g()) as i32; }`},
}

func TestSelfHostI64CallWidthIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range i64CallWidthIRCases {
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
