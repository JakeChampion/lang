package e2ecompiler

import "testing"

// nestedOptResultIRCases pin a fully-matched `Option[Result[T, E]]` (the outer
// Some bound, then the inner Result matched and its payload read) on the IR
// path. For `let o: Option[Result[..]] = Some(Ok(x))` the binding's annotation
// is o's type: an inference from the construction would type the Ok payload as
// "i32", leaving the inner `match (r)` no Result type to match on.
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 126 (cf. the wasmtime exit-code gap #2908).
var nestedOptResultIRCases = []struct {
	name string
	main string
}{
	// Some(Ok(x)) — inner Ok payload read.
	{"some-ok", `function main(): i32 { let o: Option[Result[i32, string]] = Some(Ok(5)); match (o) { Some(r) => { match (r) { Ok(n) => { return n; }, Err(e) => { return 0; } } }, None => { return 0; } } }`},
	// Some(Err(s)) — inner Err payload (string) read.
	{"some-err", `function main(): i32 { let o: Option[Result[i32, string]] = Some(Err("ab")); match (o) { Some(r) => { match (r) { Ok(n) => { return n; }, Err(e) => { return e.len(); } } }, None => { return 7; } } }`},
	// Option[Option[T]] regression (Some payload types cleanly).
	{"opt-opt-regress", `function main(): i32 { let o: Option[Option[i32]] = Some(Some(5)); match (o) { Some(r) => { match (r) { Some(n) => { return n; }, None => { return 0; } } }, None => { return 0; } } }`},
	// Unannotated Some(scalar) regression: the Option type is still inferred.
	{"unannot-some-regress", `function main(): i32 { let o = Some(7); match (o) { Some(n) => { return n; }, None => { return 0; } } }`},
}

// TestSelfHostNestedOptResultIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
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
