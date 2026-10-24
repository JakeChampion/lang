package e2ecompiler

import "testing"

var nestedOptResultIRCases = []struct {
	name string
	main string
}{
	{"some-ok", `function main(): i32 { let o: Option[Result[i32, string]] = Some(Ok(5)); match (o) { Some(r) => { match (r) { Ok(n) => { return n; }, Err(e) => { return 0; } } }, None => { return 0; } } }`},
	{"some-err", `function main(): i32 { let o: Option[Result[i32, string]] = Some(Err("ab")); match (o) { Some(r) => { match (r) { Ok(n) => { return n; }, Err(e) => { return e.len(); } } }, None => { return 7; } } }`},
	{"opt-opt-regress", `function main(): i32 { let o: Option[Option[i32]] = Some(Some(5)); match (o) { Some(r) => { match (r) { Some(n) => { return n; }, None => { return 0; } } }, None => { return 0; } } }`},
	{"unannot-some-regress", `function main(): i32 { let o = Some(7); match (o) { Some(n) => { return n; }, None => { return 0; } } }`},
}

func TestSelfHostNestedOptResultIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range nestedOptResultIRCases {
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
