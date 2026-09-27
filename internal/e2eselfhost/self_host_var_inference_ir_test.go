package e2eselfhost

import "testing"

// varInferenceIRCases exercise `var x = expr;` type inference (no explicit
// `: T` annotation) through the self-host IR path on x86-64 + wasm — beyond the
// i32 path, into the wider scalars (i64 / u32 / u8-wrap / f64 / f32 / bool /
// string) and the composites (tuple / struct / array / enum), plus inference
// from a function call's return type. The inferred local's type drives the
// arithmetic / dispatch that follows, so a wrong inference would either
// mis-lower or bail.
//
// This pins the wider-type half of the "var x: T = expr + type inference" audit
// row (docs/FEATURE-AUDIT.md); the i32 path is covered elsewhere. Each
// case is oracle-checked against the interpreter and returns a value <= 120
// (wasmtime exit-code truncation, cf. #2908).
var varInferenceIRCases = []struct {
	name string
	main string
}{
	// `var n = 7 as i64` infers i64; i64 arithmetic -> 10.
	{"infer-i64", `function main(): i32 { var n = 7 as i64; var m = n + (3 as i64); return m as i32; }`},
	// `var n = 100 as u32` infers u32 -> 120 (<= wasm clamp).
	{"infer-u32", `function main(): i32 { var n = 100 as u32; var m = n + (20 as u32); return m as i32; }`},
	// u8 inference wraps mod 256: 250 + 10 -> 4.
	{"infer-u8-wrap", `function main(): i32 { var n = 250 as u8; var m = n + (10 as u8); return m as i32; }`},
	// `var x = 2.5` infers f64; f64 arithmetic -> 4.0 -> 4.
	{"infer-f64", `function main(): i32 { var x = 2.5; var y = x + 1.5; return y as i32; }`},
	// `var x = 2.5 as f32` infers f32 -> 3.
	{"infer-f32", `function main(): i32 { var x = 2.5 as f32; var y = x + (0.5 as f32); return y as i32; }`},
	// `var b = (3 > 2)` infers boolean.
	{"infer-bool", `function main(): i32 { var b = (3 > 2); if (b) { return 1; } return 0; }`},
	// `var s = "hello"` infers string.
	{"infer-string", `function main(): i32 { var s = "hello"; return s.len(); }`},
	// `var t = (3, 4)` infers a tuple (i32, i32).
	{"infer-tuple", `function main(): i32 { var t = (3, 4); return t.0 + t.1; }`},
	// `var p = P { ... }` infers the struct type P.
	{"infer-struct", `struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 3, y: 4 }; return p.x + p.y; }`},
	// `var a = [10, 20, 30]` infers i32[].
	{"infer-array", `function main(): i32 { var a = [10, 20, 30]; return a[1]; }`},
	// `var e = A(7)` infers the enum type E.
	{"infer-enum", `enum E { A(i32), B } function main(): i32 { var e = A(7); return match (e) { A(n) => n, B => 0 }; }`},
	// Inference from a call's return type: `var n = ret()` where ret(): i64.
	{"infer-from-call", `function ret(): i64 { return 9 as i64; } function main(): i32 { var n = ret(); return n as i32; }`},
}

// TestSelfHostVarInferenceIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
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
