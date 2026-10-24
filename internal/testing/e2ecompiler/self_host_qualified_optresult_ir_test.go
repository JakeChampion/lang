package e2ecompiler

import "testing"

var qualifiedOptResultIRCases = []struct {
	name string
	main string
}{
	{"option-some", "function f(): Option[i32] { return Option.Some(42); }\nfunction main(): i32 { let o = f(); match (o) { Some(n) => { return n; }, None => { return 0; } } }"},
	{"option-none", "function f(): Option[i32] { return Option.None; }\nfunction main(): i32 { let o = f(); match (o) { Some(n) => { return n; }, None => { return 7; } } }"},
	{"result-ok", "function f(): Result[i32, string] { return Result.Ok(13); }\nfunction main(): i32 { let r = f(); match (r) { Ok(n) => { return n; }, Err(e) => { return 0; } } }"},
	{"result-err", "function f(): Result[i32, string] { return Result.Err(\"bad\"); }\nfunction main(): i32 { let r = f(); match (r) { Ok(n) => { return n; }, Err(e) => { return e.len(); } } }"},
	{"qual-with-try", "function g(): Result[i32, string] { return Result.Ok(20); }\nfunction f(): Result[i32, string] { let n = g()?; return Result.Ok(n + 5); }\nfunction main(): i32 { let r = f(); match (r) { Ok(n) => { return n; }, Err(e) => { return 0; } } }"},
}

func TestSelfHostQualifiedOptResultIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range qualifiedOptResultIRCases {
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
