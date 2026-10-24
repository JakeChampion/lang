package e2ecompiler

import "testing"

var u64MixWidthIRCases = []struct {
	name string
	main string
}{
	{"u32-ident", `function main(): i32 { let u: u32 = 2; let s: u64 = 40; return (s + u) as i32; }`},
	{"u32-loop", `function main(): i32 { let s: u64 = 0; let u: u32 = 7; for i in 0..3 { s = s + u; } return s as i32; }`},
	{"u32-mul", `function main(): i32 { let u: u32 = 6; let s: u64 = 0; return (s + u * u) as i32; }`},
	{"u32-field", `struct P { x: u32 } function main(): i32 { let p: P = P { x: 12 }; let s: u64 = 30; return (s + p.x) as i32; }`},
	{"u32-field-lhs", `struct P { x: u32 } function main(): i32 { let p: P = P { x: 12 }; let s: u64 = 30; return (p.x + s) as i32; }`},
	{"u32-field-static-mixed", `struct P { a: i32, x: u32, on: boolean } function main(): i32 { let p: P = P { a: 0 - 5, x: 40, on: true }; let s: u64 = 7; if (p.on) { return (s + p.x) as i32 + p.a; } return 0; }`},
	{"u32-field-static-distinct", `struct P { x: u32 } function main(): i32 { let p: P = P { x: 30 }; let q: P = P { x: 12 }; let s: u64 = 0; return (s + p.x + q.x) as i32; }`},
	{"u32-field-heap-high", `struct P { x: u32 } function main(): i32 { let p: P = P { x: 4000000000 }; let s: u64 = 0; if (s + p.x == 4000000000) { return 42; } return 7; }`},
	{"u32-field-heap-hex", `struct P { x: u32 } function main(): i32 { let p: P = P { x: 0x1E }; let s: u64 = 12; return (s + p.x) as i32; }`},
	{"u32-field-heap-param", `struct P { x: u32 } @noinline function mk(v: u32): P { return P { x: v }; } function main(): i32 { let p: P = mk(12); let s: u64 = 30; return (s + p.x) as i32; }`},
	{"u32-field-heap-param-high", `struct P { x: u32 } @noinline function mk(v: u32): P { return P { x: v }; } function main(): i32 { let p: P = mk(4000000000); let s: u64 = 0; if (p.x + s == 4000000000) { return 42; } return 7; }`},
	{"u32-call-high", `@noinline function f(): u32 { return 4000000000; } function main(): i32 { let s: u64 = 5; if (s + f() == 4000000005) { return 42; } return 7; }`},
	{"u32-tuple", `function main(): i32 { let t: (u32, u32) = (12, 7); let s: u64 = 30; return (s + t.0) as i32; }`},
	{"u8-ident", `function main(): i32 { let b: u8 = 200; let s: u64 = 30; if (s + b == 230) { return 42; } return 7; }`},
	{"u8-ident-u32-high", `function main(): i32 { let b: u8 = 200; let s: u32 = 4000000000; if (s + b == 4000000200) { return 42; } return 7; }`},
	{"u8-field-u32", `struct P { x: u8 } function main(): i32 { let p: P = P { x: 12 }; let s: u32 = 30; return (s + p.x) as i32; }`},
	{"u8-payload", `@noinline function f(b: u8): Result[u64, string] { return Ok(b); } function main(): i32 { match (f(42)) { Ok(v) => { return v as i32; }, Err(_) => { return 1; } } }`},
	{"u8-field", `struct P { x: u8 } function main(): i32 { let p: P = P { x: 12 }; let s: u64 = 30; return (s + p.x) as i32; }`},
	{"u8-field-lhs-bound", `struct P { x: u8 } function main(): i32 { let p: P = P { x: 12 }; let s: u64 = 30; let r: u64 = p.x + s; return r as i32; }`},
	{"u8-field-heap-max", `struct P { x: u8 } @noinline function mk(v: u8): P { return P { x: v }; } function main(): i32 { let p: P = mk(255); let s: u64 = 0; if (s + p.x == 255) { return 42; } return 7; }`},
	{"u8-tuple", `function main(): i32 { let t: (u8, u8) = (200, 7); let s: u64 = 30; if (s + t.0 == 230) { return 42; } return 7; }`},
	{"u32-arr", `function main(): i32 { let a: u32[] = [10,20,30]; let s: u64 = 0; for i in 0..3 { s = s + a[i]; } return s as i32; }`},
	{"i64-i32-keep", `function main(): i32 { let i: i32 = 2; let s: i64 = 40; return (s + i) as i32; }`},
	{"i64-i32-field-neg", `struct P { x: i32 } @noinline function mk(v: i32): P { return P { x: v }; } function main(): i32 { let p: P = mk(0 - 8); let s: i64 = 50; return (s + p.x) as i32; }`},
}

func TestSelfHostU64MixWidthIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range u64MixWidthIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target, "FERN_STRICT_IR=1"); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
