package e2ecompiler

import "testing"

var structTupleFieldIRCases = []struct {
	name string
	main string
}{
	{"nested-deep", `struct P { t: (i32, (i32, i32)) } function main(): i32 { let p = P{t: (1, (2, 3))}; return p.t.1.1; }`},
	{"nested-sum", `struct P { t: (i32, (i32, i32)) } function main(): i32 { let p = P{t: (1, (2, 3))}; return p.t.0 + p.t.1.0 + p.t.1.1; }`},
	{"tuple-option", `struct P { t: (Option[i32], i32) } function main(): i32 { let p = P{t: (Some(5), 3)}; match (p.t.0) { Some(n) => { return n + p.t.1; }, None => { return 0; } } }`},
	{"tuple-result", `struct P { t: (Result[i32, string], i32) } function main(): i32 { let p = P{t: (Ok(5), 3)}; match (p.t.0) { Ok(n) => { return n + p.t.1; }, Err(e) => { return 0; } } }`},
	{"flat-regress", `struct P { t: (i32, i32) } function main(): i32 { let p = P{t: (5, 6)}; return p.t.0 + p.t.1; }`},
	{"tuple-strarr", `struct P { t: (i32, string[]) } function main(): i32 { let p = P{t: (5, ["a", "bb"])}; return p.t.0 + p.t.1.len(); }`},
	{"tuple-i32arr", `struct P { t: (i32, i32[]) } function main(): i32 { let p = P{t: (5, [10, 20, 30])}; return p.t.0 + p.t.1.len() + p.t.1[2]; }`},
	{"tuple-strarr-elem", `struct P { t: (i32, string[]) } function main(): i32 { let p = P{t: (5, ["a", "bbb"])}; return p.t.0 + p.t.1[1].len(); }`},
	{"tuple-f64arr", `struct P { n: i32, t: (i32, f64[]) } function main(): i32 { let p = P{n: 3, t: (5, [1.0, 2.0])}; return p.n + p.t.0 + p.t.1.len(); }`},
	{"tuple-structarr", `struct Inner { a: i32 } struct P { t: (i32, Inner[]) } function main(): i32 { let p = P{t: (5, [Inner{a:1}, Inner{a:2}])}; return p.t.0 + p.t.1.len() + p.t.1[1].a; }`},
	{"tuple-enumarr", `enum Color { Red, Green, Blue } struct P { t: (i32, Color[]) } function main(): i32 { let p = P{t: (5, [Color.Red, Color.Green])}; return p.t.0 + p.t.1.len(); }`},
	{"tuple-enumarr-match", `enum Color { Red, Green, Blue } struct P { t: (i32, Color[]) } function main(): i32 { let p = P{t: (5, [Color.Red, Color.Blue])}; let s = p.t.0 + p.t.1.len(); match (p.t.1[1]) { Color.Blue => { s = s + 100; }, _ => {} } return s; }`},
}

func TestSelfHostStructTupleFieldIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range structTupleFieldIRCases {
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
