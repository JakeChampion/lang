package e2ecompiler

import "testing"

var subwordWrapIRCases = []struct {
	name string
	main string
}{
	{"u8-add-wrap", `function main(): i32 { let a: u8 = 255 as u8; let b: u8 = 1 as u8; let s: u8 = a + b; if ((s as i32) == 0) { return 5; } return 9; }`},
	{"u8-mul-wrap", `function main(): i32 { let a: u8 = 16 as u8; let b: u8 = 16 as u8; let s: u8 = a * b; if ((s as i32) == 0) { return 5; } return 9; }`},
	{"u8-shl-wrap", `function main(): i32 { let a: u8 = 1 as u8; let s: u8 = a << (8 as u8); if ((s as i32) == 0) { return 5; } return 9; }`},
	{"u8-add-exact", `function main(): i32 { let a: u8 = 60 as u8; let b: u8 = 40 as u8; return (a + b) as i32; }`},
	{"u8-sub-wrap", `function main(): i32 { let a: u8 = 0 as u8; let b: u8 = 1 as u8; let s: u8 = a - b; if ((s as i32) == 255) { return 5; } return 9; }`},

	{"u8-field-add-wrap", `struct P { x: u8, y: u8 } function main(): i32 { let p: P = P { x: 200, y: 100 }; let r: u8 = p.x + p.y; if ((r as i32) == 44) { return 5; } return 9; }`},
	{"u8-tuple-shl-wrap", `function main(): i32 { let t: (u8, u8) = (200, 1); if (((t.0 << t.1) as i32) == 144) { return 5; } return 9; }`},

	{"u8-local-mul-in-u64", `function main(): i32 { let b: u8 = 200; let s: u64 = 0; if ((s + b * 2u8) == 144) { return 5; } return 9; }`},
	{"u8-local-add-in-u64", `function main(): i32 { let b: u8 = 200; let c: u8 = 100; let s: u64 = 0; if ((s + (b + c)) == 44) { return 5; } return 9; }`},
	{"u8-local-sub-in-u64", `function main(): i32 { let b: u8 = 1; let c: u8 = 2; let s: u64 = 0; if ((s + (b - c)) == 255) { return 5; } return 9; }`},
	{"u8-local-shl-in-u64", `function main(): i32 { let b: u8 = 200; let s: u64 = 0; if ((s + (b << 1u8)) == 144) { return 5; } return 9; }`},
	{"u8-field-mul-in-u64", `struct P { x: u8 } function main(): i32 { let p: P = P { x: 200 }; let s: u64 = 0; if ((s + p.x * 2u8) == 144) { return 5; } return 9; }`},
	{"u8-field-add-in-u64", `struct P { x: u8, y: u8 } function main(): i32 { let p: P = P { x: 200, y: 100 }; let s: u64 = 0; if ((s + (p.x + p.y)) == 44) { return 5; } return 9; }`},
	{"u8-tuple-mul-in-u64", `function main(): i32 { let t: (u8, u8) = (200, 7); let s: u64 = 0; if ((s + t.0 * 2u8) == 144) { return 5; } return 9; }`},
	{"u8-tuple-sub-in-u64", `function main(): i32 { let t: (u8, u8) = (1, 2); let s: u64 = 0; if ((s + (t.0 - t.1)) == 255) { return 5; } return 9; }`},
	{"u8-exact-in-u64", `function main(): i32 { let b: u8 = 20; let c: u8 = 10; let s: u64 = 1000; if ((s + (b + c)) == 1030) { return 5; } return 9; }`},

	{"u8-local-mul-in-u32", `function main(): i32 { let b: u8 = 200; let s: u32 = 0; if ((s + b * 2u8) == 144) { return 5; } return 9; }`},
	{"u8-local-shl-in-u32", `function main(): i32 { let b: u8 = 200; let s: u32 = 0; if ((s + (b << 1u8)) == 144) { return 5; } return 9; }`},
	{"u8-field-add-in-u32", `struct P { x: u8, y: u8 } function main(): i32 { let p: P = P { x: 200, y: 100 }; let s: u32 = 0; if ((s + (p.x + p.y)) == 44) { return 5; } return 9; }`},
	{"u8-tuple-sub-in-u32", `function main(): i32 { let t: (u8, u8) = (1, 2); let s: u32 = 0; if ((s + (t.0 - t.1)) == 255) { return 5; } return 9; }`},
	{"u8-exact-in-u32", `function main(): i32 { let b: u8 = 20; let c: u8 = 10; let s: u32 = 1000; if ((s + (b + c)) == 1030) { return 5; } return 9; }`},

	{"i32-add-wrap-in-i64", `function main(): i32 { let i: i32 = 2147483647; let s: i64 = 0; if ((s + (i + 1)) == (0 - 2147483648)) { return 5; } return 9; }`},
	{"u32-add-wrap-in-u64", `function main(): i32 { let i: u32 = 4294967295; let s: u64 = 0; if ((s + (i + 1)) == 0) { return 5; } return 9; }`},
	{"narrow-checked-in-i64", `function narrow_chk(s: i64, a: i32, b: i32): Option[i64] { return Some(s + ((a +? b)?)); } function main(): i32 { match (narrow_chk(10, 2147483647, 1)) { Some(_) => { return 9; }, None => {} } match (narrow_chk(10, 0 - 30, 0 - 40)) { Some(v) => { if (v == (0 - 60)) { return 5; } return 9; }, None => { return 9; } } }`},
}

func TestSelfHostSubwordWrapIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range subwordWrapIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
