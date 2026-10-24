package e2ecompiler

import "testing"

var i64FieldWidthIRCases = []struct {
	name string
	main string
}{
	{"struct-field", `struct P { x: i32 } function main(): i32 { let p: P = P { x: 12 }; let s: i64 = 30; return (s + p.x) as i32; }`},
	{"struct-neg", `struct P { x: i32 } function main(): i32 { let p: P = P { x: -8 }; let s: i64 = 50; return (s + p.x) as i32; }`},
	{"struct-two", `struct P { x: i32, y: i32 } function main(): i32 { let p: P = P { x: 20, y: 22 }; let s: i64 = 0; return (s + p.x + p.y) as i32; }`},
	{"tuple-elem", `function main(): i32 { let t: (i32, i32) = (12, 7); let s: i64 = 30; return (s + t.0) as i32; }`},
	{"tuple-neg", `function main(): i32 { let t: (i32, i32) = (-8, 1); let s: i64 = 50; return (s + t.0) as i32; }`},
	{"struct-i64-keep", `struct P { x: i64 } function main(): i32 { let p: P = P { x: 42 }; let s: i64 = 0; return (s + p.x) as i32; }`},
	{"tuple-i64-keep", `function main(): i32 { let t: (i64, i32) = (42, 1); let s: i64 = 0; return (s + t.0) as i32; }`},
}

func TestSelfHostI64FieldWidthIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range i64FieldWidthIRCases {
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
