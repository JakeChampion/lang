package e2eselfhost

import "testing"

// optUnusedWideBindIRCases pin an Option/Result match-EXPRESSION whose arm binds an
// i64/f64 payload to a name the arm body never references (`Some(x) => 1`) to the
// self-host IR path on x86-64 + wasm. The StmtMatch binder already produces the
// correct width-typed payload slot, and an i32 (or wildcard `Some(_)`) binding
// already lowered; only the eligibility gate `iife_payload_field_bindable` rejected
// an i64/f64 payload bound to an unread name (its result temp was conservatively
// assumed to need the payload width), bailing the whole module to the legacy AST
// emitter. #2691 admits a DEAD wide binding (the arm never mentions the name, so the
// payload width is irrelevant to the result temp). Each case is oracle-checked
// against the interpreter and returns <= 126. Mirrors self_host_iife_i64_annot_ir_test.go.
var optUnusedWideBindIRCases = []struct {
	name string
	main string
}{
	// Option[f64], Some arm binds an unused x, returns a constant. 1.
	{"opt-f64-unused", `function main(): i32 { var o: Option[f64] = Some(3.5); return match (o) { Some(x) => 1, None => 0 }; }`},
	// Option[f64] = None — the None arm taken (distinct exit). 7.
	{"opt-f64-none-taken", `function main(): i32 { var o: Option[f64] = None; return match (o) { Some(x) => 1, None => 7 }; }`},
	// Result[f64, i32], Ok arm binds an unused x. 1.
	{"result-f64-unused", `function main(): i32 { var r: Result[f64, i32] = Ok(3.5); return match (r) { Ok(x) => 1, Err(e) => 0 }; }`},
	// Option[i64] (8-byte payload) bound to an unread name. 4.
	{"opt-i64-unused", `function main(): i32 { var o: Option[i64] = Some(9000000000); return match (o) { Some(x) => 4, None => 0 }; }`},
	// The match-expression result bound into a local, unused f64 payload. 1.
	{"opt-f64-var-bind", `function main(): i32 { var o: Option[f64] = Some(3.5); var r: i32 = match (o) { Some(x) => 1, None => 0 }; return r; }`},
	// Regression: a USED f64 binding (`Some(x) => x as i32`) was already on the IR
	// path via the wide-read classifier — it must stay there. 3.5 as i32 = 3.
	{"opt-f64-used", `function main(): i32 { var o: Option[f64] = Some(3.5); return match (o) { Some(x) => x as i32, None => 0 }; }`},
}

// TestSelfHostOptUnusedWideBindIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostOptUnusedWideBindIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range optUnusedWideBindIRCases {
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
