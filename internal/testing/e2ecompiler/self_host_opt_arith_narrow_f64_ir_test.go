package e2ecompiler

import "testing"

var optArithNarrowF64IRCases = []struct {
	name string
	main string
}{
	{"mul-narrow", `function main(): i32 { let o: Option[f64] = Some(2.5); return match (o) { Some(v) => (v * 4.0) as i32, None => 0 }; }`},
	{"add-narrow", `function main(): i32 { let o: Option[f64] = Some(2.5); return match (o) { Some(v) => (v + 1.5) as i32, None => 0 }; }`},
	{"result-add", `function main(): i32 { let r: Result[f64, i32] = Ok(3.5); return match (r) { Ok(v) => (v + 1.0) as i32, Err(e) => e }; }`},
	{"none-taken", `function main(): i32 { let o: Option[f64] = None; return match (o) { Some(v) => (v * 4.0) as i32, None => 7 }; }`},
	{"compound", `function main(): i32 { let o: Option[f64] = Some(3.0); return match (o) { Some(v) => (v * 2.0 + 1.0) as i32, None => 0 }; }`},
	{"i64-keep", `function main(): i32 { let o: Option[i64] = Some(40); return match (o) { Some(v) => (v + 2) as i32, None => 0 }; }`},
}

func TestSelfHostOptArithNarrowF64IR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range optArithNarrowF64IRCases {
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
