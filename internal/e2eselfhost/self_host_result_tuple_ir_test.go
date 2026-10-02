package e2eselfhost

import "testing"

// resultTupleIRCases close the comma-containing-tuple-element case (after nested
// tuples): a `Result[T, E]` element of a tuple — `(1, Ok(5))`, accessed via
// `match (t.1)` — now lowers on the IR path. `Result[T, E]` has an internal comma
// but is bracketed, so the now-depth-aware tag decoders keep it whole; the only
// remaining blockers were the explicit "Option-only" exclusions. The full
// `Result[T, E]` tag (a bare `Ok(x)` cannot name E) comes from the binding's
// tuple TYPE annotation — and the checker rejects an un-annotated Result, so the
// annotation is always present. `opt_payload_type` already recovers both arms.
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 126 (cf. the wasmtime exit-code gap #2908).
var resultTupleIRCases = []struct {
	name string
	main string
}{
	// Ok payload of a Result tuple element round-trips through a match.
	{"result-ok-elem", `function main(): i32 { let t: (i32, Result[i32, string]) = (1, Ok(5)); match (t.1) { Ok(n) => { return n; }, Err(e) => { return 0; } } }`},
	// Err payload (a string) reaches the Err arm.
	{"result-err-elem", `function main(): i32 { let t: (i32, Result[i32, string]) = (1, Err("ab")); match (t.1) { Ok(n) => { return n; }, Err(e) => { return e.len(); } } }`},
	// Result as the FIRST element, with a scalar sibling.
	{"result-first-elem", `function main(): i32 { let t: (Result[i32, string], i32) = (Ok(7), 3); match (t.0) { Ok(n) => { return n + t.1; }, Err(e) => { return 0; } } }`},
	// Returned from a function (the tag comes from the return-type annotation).
	{"result-ret-tuple", "function f(): (i32, Result[i32, string]) { return (9, Ok(5)); }\nfunction main(): i32 { let t = f(); match (t.1) { Ok(n) => { return t.0 + n; }, Err(e) => { return 0; } } }"},
	// Option-in-tuple regression (must stay on the IR path).
	{"option-elem-regress", `function main(): i32 { let t: (i32, Option[i32]) = (1, Some(5)); match (t.1) { Some(n) => { return n; }, None => { return 0; } } }`},
}

// TestSelfHostResultTupleIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
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
