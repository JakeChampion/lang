package e2ecompiler

import "testing"

var errorTraitIRCases = []struct {
	name     string
	src      string
	expected int
}{
	{"basic-error-dyn-dispatch",
		`trait Error { function code(self: Self): i32; } struct NotFound { n: i32 } impl Error for NotFound { function code(self: Self): i32 { return self.n; } } function find(ok: boolean): Result[i32, NotFound] { if (ok) { return Ok(42); } return Err(NotFound { n: 7 }); } function handler(ok: boolean): Result[i32, dyn Error] { let v: i32 = find(ok)?; return Ok(v + 1); } function main(): i32 { let result: i32 = 0; match (handler(false)) { Ok(v) => { result = v; }, Err(e) => { result = e.code(); } } return result; }`,
		7},

	{"error-ok-path",
		`trait Error { function code(self: Self): i32; } struct NotFound { n: i32 } impl Error for NotFound { function code(self: Self): i32 { return self.n; } } function find(ok: boolean): Result[i32, NotFound] { if (ok) { return Ok(42); } return Err(NotFound { n: 7 }); } function handler(ok: boolean): Result[i32, dyn Error] { let v: i32 = find(ok)?; return Ok(v + 1); } function main(): i32 { let result: i32 = 0; match (handler(true)) { Ok(v) => { result = v; }, Err(e) => { result = e.code(); } } return result; }`,
		43},

	{"iife-err-arm-dyn-dispatch",
		`trait Error { function code(self: Self): i32; } struct NotFound { n: i32 } impl Error for NotFound { function code(self: Self): i32 { return self.n; } } function find(ok: boolean): Result[i32, NotFound] { if (ok) { return Ok(42); } return Err(NotFound { n: 7 }); } function handler(ok: boolean): Result[i32, dyn Error] { let v: i32 = find(ok)?; return Ok(v + 1); } function main(): i32 { let a: i32 = match (handler(true)) { Ok(v) => v, Err(e) => e.code() }; let b: i32 = match (handler(false)) { Ok(v) => v, Err(e) => e.code() }; return a + b; }`,
		50},

	{"two-impls-dispatch",
		`trait Error { function code(self: Self): i32; } struct NotFound { n: i32 } struct PermError { c: i32 } impl Error for NotFound { function code(self: Self): i32 { return self.n; } } impl Error for PermError { function code(self: Self): i32 { return self.c; } } function find_nf(): Result[i32, NotFound] { return Err(NotFound { n: 3 }); } function find_pe(): Result[i32, PermError] { return Err(PermError { c: 5 }); } function run_nf(): Result[i32, dyn Error] { let v: i32 = find_nf()?; return Ok(v); } function run_pe(): Result[i32, dyn Error] { let v: i32 = find_pe()?; return Ok(v); } function main(): i32 { let a: i32 = 0; let b: i32 = 0; match (run_nf()) { Ok(v) => { a = v; }, Err(e) => { a = e.code(); } } match (run_pe()) { Ok(v) => { b = v; }, Err(e) => { b = e.code(); } } return a + b; }`,
		8},

	{"error-message-string",
		`trait Error { function message(self: Self): string; } struct NotFound { n: i32 } impl Error for NotFound { function message(self: Self): string { return "not found"; } } function find(ok: boolean): Result[i32, NotFound] { if (ok) { return Ok(1); } return Err(NotFound { n: 0 }); } function handler(ok: boolean): Result[i32, dyn Error] { let v: i32 = find(ok)?; return Ok(v); } function main(): i32 { let msg_len: i32 = 0; match (handler(false)) { Ok(v) => { msg_len = 0; }, Err(e) => { let m: string = e.message(); msg_len = m.len(); } } return msg_len; }`,
		9},

	{"error-code-and-message",
		`trait Error { function code(self: Self): i32; function message(self: Self): string; } struct Fail { c: i32 } impl Error for Fail { function code(self: Self): i32 { return self.c; } function message(self: Self): string { return "oops"; } } function run(): Result[i32, Fail] { return Err(Fail { c: 4 }); } function handler(): Result[i32, dyn Error] { let v: i32 = run()?; return Ok(v); } function main(): i32 { let result: i32 = 0; match (handler()) { Ok(v) => { result = v; }, Err(e) => { let m: string = e.message(); result = e.code() + m.len(); } } return result; }`,
		8},
}

func TestSelfHostErrorTraitIR(t *testing.T) {
	boxedProbes(t)
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range errorTraitIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				stderr, code := cli.exitOf(t, tc.src, target, "FERN_LEAKCHECK=1")
				if code != tc.expected {
					t.Fatalf("exited %d, want %d\n%s", code, tc.expected, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}
