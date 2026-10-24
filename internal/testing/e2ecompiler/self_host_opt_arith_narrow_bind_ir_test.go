package e2ecompiler

import "testing"

var optArithNarrowBindIRCases = []struct {
	name string
	main string
}{
	{"add-narrow", `function main(): i32 { let o: Option[i64] = Some(40); return match (o) { Some(x) => (x + 2) as i32, None => 0 }; }`},
	{"mul-narrow", `function main(): i32 { let o: Option[i64] = Some(40); return match (o) { Some(x) => (x * 2) as i32, None => 0 }; }`},
	{"none-taken", `function main(): i32 { let o: Option[i64] = None; return match (o) { Some(x) => (x + 2) as i32, None => 7 }; }`},
	{"result-sub", `function main(): i32 { let r: Result[i64, i32] = Ok(40); return match (r) { Ok(x) => (x - 5) as i32, Err(e) => 0 }; }`},
	{"bare-narrow", `function main(): i32 { let o: Option[i64] = Some(40); return match (o) { Some(x) => x as i32, None => 0 }; }`},
}

func TestSelfHostOptArithNarrowBindIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range optArithNarrowBindIRCases {
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
