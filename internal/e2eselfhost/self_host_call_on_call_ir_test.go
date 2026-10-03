package e2eselfhost

import "testing"

// callOnCallIRCases exercise calling the RESULT of a call — `mk()(args)`, where
// `mk` returns a function value — through the self-host IR path on x86-64 + wasm.
//
// Every function value is an env box, so the call on the result dispatches
// env-first: the inner call's box is stashed, passed as the __env first
// argument, and box[0] is the target (#10057). Binding the result first and
// calling an element of a function array take the same form.
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 126 (wasmtime exit-code truncation, cf. #2908).
var callOnCallIRCases = []struct {
	name string
	main string
}{
	// mk() returns `b -> b+1`; calling it inline with 4 = 5.
	{"nocap", `function mk(): (i32) => i32 { return (b: i32): i32 => { return b + 1; }; }
function main(): i32 { return mk()(4); }`},
	// Result fed into arithmetic: (5*3) + 1 = 16.
	{"in-expr", `function mk(): (i32) => i32 { return (b: i32): i32 => { return b * 3; }; }
function main(): i32 { return mk()(5) + 1; }`},
	// Two-argument returned lambda, called inline: 5 + 6 = 11.
	{"two-arg", `function mk(): (i32, i32) => i32 { return (a: i32, b: i32): i32 => { return a + b; }; }
function main(): i32 { return mk()(5, 6); }`},
	// Returned lambda called inline twice: (4+1) + (10+1) = 16.
	{"twice", `function mk(): (i32) => i32 { return (b: i32): i32 => { return b + 1; }; }
function main(): i32 { return mk()(4) + mk()(10); }`},
	// Regression: binding the result first still lowers (4 + 1 = 5).
	{"bind-regress", `function mk(): (i32) => i32 { return (b: i32): i32 => { return b + 1; }; }
function main(): i32 { let g = mk(); return g(4); }`},
	// Regression: calling a function-array element still lowers (4 + 1 = 5).
	{"fnarr-regress", `function inc(b: i32): i32 { return b + 1; }
function main(): i32 { let fs: ((i32) => i32)[] = [inc]; return fs[0](4); }`},
}

// TestSelfHostCallOnCallIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostCallOnCallIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range callOnCallIRCases {
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
