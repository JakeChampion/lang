package e2ecompiler

import "testing"

// optArithNarrowF64IRCases pin an Option/Result match-EXPRESSION arm that does
// f64 ARITHMETIC over a bound f64 payload and then narrows it with `as i32`
// (`match (o) { Some(v) => (v * 4.0) as i32, None => 0 }`) to the self-host IR
// path on x86-64 + wasm. This is the f64 sibling of the i64 arith-narrow admit
// (#3585): the arm computes a wide f64 intermediate over the payload and casts
// the whole thing to i32, so the result temp is i32 (the cast narrows) exactly
// like the i64 case. iife_arm_returns_narrowed_payload_arith was gated `ft ==
// "i64"`; #2691 extends it (and its call site in iife_payload_field_bindable) to
// f64, passing kind = ft so the f64 arith leaf/op classifier
// (iife_payload_arith_kind / _leaf_kind, which already handle f64) fires. Each
// case is oracle-checked against the interpreter and returns <= 126.
var optArithNarrowF64IRCases = []struct {
	name string
	main string
}{
	// (payload * 4.0) as i32. 2.5 * 4.0 = 10.
	{"mul-narrow", `function main(): i32 { let o: Option[f64] = Some(2.5); return match (o) { Some(v) => (v * 4.0) as i32, None => 0 }; }`},
	// (payload + 1.5) as i32. 2.5 + 1.5 = 4.
	{"add-narrow", `function main(): i32 { let o: Option[f64] = Some(2.5); return match (o) { Some(v) => (v + 1.5) as i32, None => 0 }; }`},
	// Result[f64, i32], Ok arm arith-then-narrow. 3.5 + 1.0 = 4.
	{"result-add", `function main(): i32 { let r: Result[f64, i32] = Ok(3.5); return match (r) { Ok(v) => (v + 1.0) as i32, Err(e) => e }; }`},
	// None arm taken — the arith arm is not evaluated. 7.
	{"none-taken", `function main(): i32 { let o: Option[f64] = None; return match (o) { Some(v) => (v * 4.0) as i32, None => 7 }; }`},
	// A compound f64 arith composition over the payload. 3.0 * 2.0 + 1.0 = 7.
	{"compound", `function main(): i32 { let o: Option[f64] = Some(3.0); return match (o) { Some(v) => (v * 2.0 + 1.0) as i32, None => 0 }; }`},
	// Regression: the i64 arith-narrow (already on the IR path) still works. 42.
	{"i64-keep", `function main(): i32 { let o: Option[i64] = Some(40); return match (o) { Some(v) => (v + 2) as i32, None => 0 }; }`},
}

// TestSelfHostOptArithNarrowF64IR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostOptArithNarrowF64IR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range optArithNarrowF64IRCases {
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
