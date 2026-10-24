package e2ecompiler

import "testing"

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
	{"ok-prop", `function run(): Result[i32, Ferr] { let v = step(1)?; return Ok(v + 1); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 6},
	{"err-prop-code", `function run(): Result[i32, Ferr] { let v = step(0)?; return Ok(v + 1); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 9},
	{"two-ok", `function run(): Result[i32, Ferr] { let a = step(1)?; let b = step(1)?; return Ok(a + b); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 10},
	{"second-errs", `function run(): Result[i32, Ferr] { let a = step(1)?; let b = step(0)?; return Ok(a + b); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code; } } }`, 9},
	{"two-field-err", `function run(): Result[i32, Ferr] { let v = step(0)?; return Ok(v); } function main(): i32 { match (run()) { Ok(v) => { return v; }, Err(e) => { return e.code + e.detail; } } }`, 11},
}

func resultStructErrIRSrc(mainBody string) string {
	return resultStructErrIRPrelude + "\n" + mainBody + "\n"
}

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
