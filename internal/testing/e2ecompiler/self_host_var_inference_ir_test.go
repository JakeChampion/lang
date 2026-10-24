package e2ecompiler

import "testing"

var varInferenceIRCases = []struct {
	name string
	main string
}{
	{"infer-i64", `function main(): i32 { let n = 7 as i64; let m = n + (3 as i64); return m as i32; }`},
	{"infer-u32", `function main(): i32 { let n = 100 as u32; let m = n + (20 as u32); return m as i32; }`},
	{"infer-u8-wrap", `function main(): i32 { let n = 250 as u8; let m = n + (10 as u8); return m as i32; }`},
	{"infer-f64", `function main(): i32 { let x = 2.5; let y = x + 1.5; return y as i32; }`},
	{"infer-f32", `function main(): i32 { let x = 2.5 as f32; let y = x + (0.5 as f32); return y as i32; }`},
	{"infer-bool", `function main(): i32 { let b = (3 > 2); if (b) { return 1; } return 0; }`},
	{"infer-string", `function main(): i32 { let s = "hello"; return s.len(); }`},
	{"infer-tuple", `function main(): i32 { let t = (3, 4); return t.0 + t.1; }`},
	{"infer-struct", `struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 3, y: 4 }; return p.x + p.y; }`},
	{"infer-array", `function main(): i32 { let a = [10, 20, 30]; return a[1]; }`},
	{"infer-enum", `enum E { A(i32), B } function main(): i32 { let e = A(7); return match (e) { A(n) => n, B => 0 }; }`},
	{"infer-from-call", `function ret(): i64 { return 9 as i64; } function main(): i32 { let n = ret(); return n as i32; }`},
}

func TestSelfHostVarInferenceIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range varInferenceIRCases {
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
