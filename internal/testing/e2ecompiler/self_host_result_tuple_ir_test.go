package e2ecompiler

import "testing"

var resultTupleIRCases = []struct {
	name string
	main string
}{
	{"result-ok-elem", `function main(): i32 { let t: (i32, Result[i32, string]) = (1, Ok(5)); match (t.1) { Ok(n) => { return n; }, Err(e) => { return 0; } } }`},
	{"result-err-elem", `function main(): i32 { let t: (i32, Result[i32, string]) = (1, Err("ab")); match (t.1) { Ok(n) => { return n; }, Err(e) => { return e.len(); } } }`},
	{"result-first-elem", `function main(): i32 { let t: (Result[i32, string], i32) = (Ok(7), 3); match (t.0) { Ok(n) => { return n + t.1; }, Err(e) => { return 0; } } }`},
	{"result-ret-tuple", "function f(): (i32, Result[i32, string]) { return (9, Ok(5)); }\nfunction main(): i32 { let t = f(); match (t.1) { Ok(n) => { return t.0 + n; }, Err(e) => { return 0; } } }"},
	{"option-elem-regress", `function main(): i32 { let t: (i32, Option[i32]) = (1, Some(5)); match (t.1) { Some(n) => { return n; }, None => { return 0; } } }`},
}

func TestSelfHostResultTupleIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range resultTupleIRCases {
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
