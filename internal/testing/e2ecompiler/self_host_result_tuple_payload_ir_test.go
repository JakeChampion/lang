package e2ecompiler

import "testing"

// resultTuplePayloadIRCases pin a `Result[T, E]` whose T or E is itself a tuple
// — e.g. `Result[(i32, i32), string]`, matched and the payload's elements read
// (`t.0` / `t.1`) — on the IR path. Splitting T from E must skip the tuple's
// inner comma: T is `(i32, i32)`, not `(i32`.
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 126 (cf. the wasmtime exit-code gap #2908).
var resultTuplePayloadIRCases = []struct {
	name string
	main string
}{
	// Ok payload is a tuple; read both elements.
	{"ok-tuple", `function main(): i32 { let r: Result[(i32, i32), string] = Ok((3, 4)); match (r) { Ok(t) => { return t.0 + t.1; }, Err(e) => { return 0; } } }`},
	// Same type, Err arm taken (string payload).
	{"err-string", `function main(): i32 { let r: Result[(i32, i32), string] = Err("ab"); match (r) { Ok(t) => { return t.0 + t.1; }, Err(e) => { return e.len(); } } }`},
	// The Err type is the tuple (mirror): exercises the E side of the split.
	{"err-tuple", `function main(): i32 { let r: Result[i32, (i32, i32)] = Err((5, 6)); match (r) { Ok(n) => { return n; }, Err(t) => { return t.0 + t.1; } } }`},
	// Scalar-Result regression (must stay on the IR path).
	{"scalar-regress", `function main(): i32 { let r: Result[i32, string] = Ok(5); match (r) { Ok(n) => { return n; }, Err(e) => { return 0; } } }`},
}

// TestSelfHostResultTuplePayloadIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
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
