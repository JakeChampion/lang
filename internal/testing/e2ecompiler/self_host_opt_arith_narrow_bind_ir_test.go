package e2ecompiler

import "testing"

// optArithNarrowBindIRCases pin an Option/Result match-EXPRESSION arm that does
// ARITHMETIC over a bound i64 payload and then narrows it with `as i32`
// (`match (o) { Some(x) => (x + 2) as i32, None => 0 }`) to the self-host IR path on
// x86-64 + wasm (#2691). The match-expression result is i32 (the cast narrows),
// but an arm computes a wide i64 intermediate over the payload first. Each case
// is oracle-checked against the interpreter and returns <= 126. Mirrors
// self_host_opt_unused_wide_bind_ir_test.go.
var optArithNarrowBindIRCases = []struct {
	name string
	main string
}{
	// (payload + 2) as i32. 40 + 2 = 42.
	{"add-narrow", `function main(): i32 { let o: Option[i64] = Some(40); return match (o) { Some(x) => (x + 2) as i32, None => 0 }; }`},
	// (payload * 2) as i32. 40 * 2 = 80.
	{"mul-narrow", `function main(): i32 { let o: Option[i64] = Some(40); return match (o) { Some(x) => (x * 2) as i32, None => 0 }; }`},
	// None arm taken — the arith arm is not evaluated. 7.
	{"none-taken", `function main(): i32 { let o: Option[i64] = None; return match (o) { Some(x) => (x + 2) as i32, None => 7 }; }`},
	// Result[i64, i32], Ok arm arith-then-narrow. 40 - 5 = 35.
	{"result-sub", `function main(): i32 { let r: Result[i64, i32] = Ok(40); return match (r) { Ok(x) => (x - 5) as i32, Err(e) => 0 }; }`},
	// Regression: the bare-payload narrow (`x as i32`) was already on the IR path. 40.
	{"bare-narrow", `function main(): i32 { let o: Option[i64] = Some(40); return match (o) { Some(x) => x as i32, None => 0 }; }`},
}

// TestSelfHostOptArithNarrowBindIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostOptArithNarrowBindIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range optArithNarrowBindIRCases {
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
