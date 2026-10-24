package e2ecompiler

import "testing"

var resultTuplePayloadIRCases = []struct {
	name string
	main string
}{
	{"ok-tuple", `function main(): i32 { let r: Result[(i32, i32), string] = Ok((3, 4)); match (r) { Ok(t) => { return t.0 + t.1; }, Err(e) => { return 0; } } }`},
	{"err-string", `function main(): i32 { let r: Result[(i32, i32), string] = Err("ab"); match (r) { Ok(t) => { return t.0 + t.1; }, Err(e) => { return e.len(); } } }`},
	{"err-tuple", `function main(): i32 { let r: Result[i32, (i32, i32)] = Err((5, 6)); match (r) { Ok(n) => { return n; }, Err(t) => { return t.0 + t.1; } } }`},
	{"scalar-regress", `function main(): i32 { let r: Result[i32, string] = Ok(5); match (r) { Ok(n) => { return n; }, Err(e) => { return 0; } } }`},
}

func TestSelfHostResultTuplePayloadIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range resultTuplePayloadIRCases {
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
