package e2eselfhost

import "testing"

// resultStructErrIRCases pin `?` error-propagation over a `Result[T, E]` whose
// error type E is a CONCRETE STRUCT (the Rust-style `Result[T, MyError]` shape),
// with field access on the bound error in the `Err` arm, on the self-host IR path
// (x86-64 + wasm). The existing `?` pins (self_host_try_op_*) use a `string`
// error; the error-trait pin (self_host_error_trait_ir) uses a `dyn Error` trait
// object — neither covers a concrete struct error. This exercises: `?` desugar
// over a struct-payload `Err`, the `Err(struct)` construction + propagation
// across the call boundary, the `Ok`/`Err` payload `match`, and struct field
// reads (`e.code`, `e.detail`) on the bound error. All already lowers, so no
// compiler change — an observability pin against a regression to the AST
// fallback.
//
// Each case is oracle-checked against the interpreter; results stay <= 120
// (the wasm exit-code clamp, #2908).
const resultStructErrIRPrelude = `struct Ferr { code: i32, detail: i32 }
function step(ok: i32): Result[i32, Ferr] {
    if (ok == 0) { return Err(Ferr { code: 9, detail: 2 }); }
    return Ok(5);
}
`

var resultStructErrIRCases = []struct {
	name string
	main string
	want int
}{
	// happy path: step(1)? unwraps Ok(5), +1 = 6.
	{"ok-prop", `function run(): Result[i32, Ferr] { var v = step(1)?; return Ok(v + 1); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 6},
	// error path: step(0)? propagates Err(Ferr); the handler reads e.code = 9.
	{"err-prop-code", `function run(): Result[i32, Ferr] { var v = step(0)?; return Ok(v + 1); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 9},
	// two `?` in a row, both Ok: 5 + 5 = 10.
	{"two-ok", `function run(): Result[i32, Ferr] { var a = step(1)?; var b = step(1)?; return Ok(a + b); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 10},
	// the second `?` short-circuits on Err; e.code = 9 from the propagated error.
	{"second-errs", `function run(): Result[i32, Ferr] { var a = step(1)?; var b = step(0)?; return Ok(a + b); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 9},
	// read TWO fields of the struct error: e.code + e.detail = 9 + 2 = 11.
	{"two-field-err", `function run(): Result[i32, Ferr] { var v = step(0)?; return Ok(v); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code + e.detail; } } }`, 11},
}

func resultStructErrIRSrc(mainBody string) string {
	return resultStructErrIRPrelude + "\n" + mainBody + "\n"
}

// TestSelfHostResultStructErrIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostResultStructErrIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range resultStructErrIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, resultStructErrIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
