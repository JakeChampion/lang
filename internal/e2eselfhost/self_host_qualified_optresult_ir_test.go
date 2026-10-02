package e2eselfhost

import "testing"

// qualifiedOptResultIRCases widen the self-host IR subset: the qualified built-in
// Option/Result construction spellings — `Option.Some(x)`, `Option.None`,
// `Result.Ok(x)`, `Result.Err(x)` — lower on the IR path alongside the bare
// forms (`Some(x)` / `Ok(x)` / `None`). A qualified construction that makes the
// whole module IR-ineligible used to fall back to the AST emitter, which
// mis-lowered it as `# unresolved ident: <Enum>`; today it is refused outright.
// The qualified forms produce the identical
// value as the bare ones (the same op_opt_make / op_opt_none box), so they share a
// `lower_opt_make_payload` helper.
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 126 (cf. the wasmtime exit-code gap #2908).
var qualifiedOptResultIRCases = []struct {
	name string
	main string
}{
	// Option.Some payload round-trips through a match.
	{"option-some", "function f(): Option[i32] { return Option.Some(42); }\nfunction main(): i32 { let o = f(); match (o) { Some(n) => { return n; }, None => { return 0; } } }"},
	// Option.None takes the None arm.
	{"option-none", "function f(): Option[i32] { return Option.None; }\nfunction main(): i32 { let o = f(); match (o) { Some(n) => { return n; }, None => { return 7; } } }"},
	// Result.Ok payload round-trips.
	{"result-ok", "function f(): Result[i32, string] { return Result.Ok(13); }\nfunction main(): i32 { let r = f(); match (r) { Ok(n) => { return n; }, Err(e) => { return 0; } } }"},
	// Result.Err payload (a string) reaches the Err arm.
	{"result-err", "function f(): Result[i32, string] { return Result.Err(\"bad\"); }\nfunction main(): i32 { let r = f(); match (r) { Ok(n) => { return n; }, Err(e) => { return e.len(); } } }"},
	// Qualified construction composes with the try-operator.
	{"qual-with-try", "function g(): Result[i32, string] { return Result.Ok(20); }\nfunction f(): Result[i32, string] { let n = g()?; return Result.Ok(n + 5); }\nfunction main(): i32 { let r = f(); match (r) { Ok(n) => { return n; }, Err(e) => { return 0; } } }"},
}

// TestSelfHostQualifiedOptResultIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
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
