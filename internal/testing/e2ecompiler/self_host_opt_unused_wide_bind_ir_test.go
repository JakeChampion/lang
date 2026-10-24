package e2ecompiler

import "testing"

var optUnusedWideBindIRCases = []struct {
	name string
	main string
}{
	{"opt-f64-unused", `function main(): i32 { let o: Option[f64] = Some(3.5); return match (o) { Some(x) => 1, None => 0 }; }`},
	{"opt-f64-none-taken", `function main(): i32 { let o: Option[f64] = None; return match (o) { Some(x) => 1, None => 7 }; }`},
	{"result-f64-unused", `function main(): i32 { let r: Result[f64, i32] = Ok(3.5); return match (r) { Ok(x) => 1, Err(e) => 0 }; }`},
	{"opt-i64-unused", `function main(): i32 { let o: Option[i64] = Some(9000000000); return match (o) { Some(x) => 4, None => 0 }; }`},
	{"opt-f64-var-bind", `function main(): i32 { let o: Option[f64] = Some(3.5); let r: i32 = match (o) { Some(x) => 1, None => 0 }; return r; }`},
	{"opt-f64-used", `function main(): i32 { let o: Option[f64] = Some(3.5); return match (o) { Some(x) => x as i32, None => 0 }; }`},
}

func TestSelfHostOptUnusedWideBindIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range optUnusedWideBindIRCases {
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
