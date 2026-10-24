package e2ecompiler

import "testing"

var i64MixWidthIRCases = []struct {
	name string
	main string
}{
	{"range-acc", `function main(): i32 { let s: i64 = 0; for i in 0..5 { s = s + i; } return s as i32; }`},
	{"range-incl", `function main(): i32 { let s: i64 = 0; for i in 1..=5 { s = s + i; } return s as i32; }`},
	{"mix-local", `function main(): i32 { let i: i32 = 2; let s: i64 = 40; return (s + i) as i32; }`},
	{"mix-param", `function f(a: i32): i64 { let s: i64 = 39; return s + a; } function main(): i32 { return f(3) as i32; }`},
	{"mix-nested", `function main(): i32 { let i: i32 = 5; let s: i64 = 30; return (s + (i + 2)) as i32; }`},
	{"mix-mul", `function main(): i32 { let i: i32 = 3; let s: i64 = 14; return (s * i) as i32; }`},
	{"neg-sign", `function main(): i32 { let i: i32 = -8; let s: i64 = 50; return (s + i) as i32; }`},
	{"big-acc", `function main(): i32 { let s: i64 = 0; for i in 0..100 { s = s + i; } return (s / 100) as i32; }`},
	{"wide-literal-compound", `function main(): i32 { let t = 3 - 4611686018427387904; let u = 4611686018427387904 - 3; if (t != 0 - u) { return 1; } return ((u / 1000000000000000000) as i32) + 40; }`},
	{"wide-literal-generic-compare", `function id[T](v: T): T { return v; } function main(): i32 { let t = id(4611686018427387904); let c = 0; if (t > 0) { c = c + 1; } if (4611686018427387904 > 1) { c = c + 2; } let b = 1 < 4611686018427387904; if (b) { c = c + 4; } if (4611686018427387904 != 0) { c = c + 8; } return c; }`},
	{"wide-literal-generic-tuple", `function pair[A, B](a: A, b: B): (A, B) { return (a, b); } function main(): i32 { let p = pair(4611686018427387904, "hello"); let c = 0; if (p.0 == 4611686018427387904 && p.1 == "hello") { c = c + 1; } if (p.0 / 1000000000000000000 == 4) { c = c + 2; } return c; }`},
}

func TestSelfHostI64MixWidthIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range i64MixWidthIRCases {
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
