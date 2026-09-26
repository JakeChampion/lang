package e2eselfhost

import "testing"

// errorTraitIRCases exercise the stdlib `Error` trait pattern. The key shapes:
//
//   - A `Result[i32, dyn Error]`-returning function that propagates a concrete
//     `E: Error` via `?`, which widens the failure payload to `dyn Error`.
//   - A statement-position `match` binding `Err(e)` where `e: dyn Error`, and
//     `e.method()` dispatching on it.
//   - A value-position `match` binding `Err(e)` into an i32 temp.
//   - Methods on `dyn Error` that return i32 (`code()`) and `string`
//     (`message()`, bound with `var m: string = e.message()`).
//
// Exit codes are the oracle.
var errorTraitIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Basic Error trait dispatch: a single impl, ? propagation, statement match.
	// find(false) returns Err(NotFound{n:7}), handler forwards it as dyn Error,
	// the match binds e and calls e.code() = 7.
	{"basic-error-dyn-dispatch",
		`trait Error { function code(self: Self): i32; } struct NotFound { n: i32 } impl Error for NotFound { function code(self: Self): i32 { return self.n; } } function find(ok: boolean): Result[i32, NotFound] { if (ok) { return Ok(42); } return Err(NotFound { n: 7 }); } function handler(ok: boolean): Result[i32, dyn Error] { var v: i32 = find(ok)?; return Ok(v + 1); } function main(): i32 { var result: i32 = 0; match (handler(false)) { Ok(v) => { result = v; }, Err(e) => { result = e.code(); } } return result; }`,
		7},

	// Ok path: handler(true) → Ok(43); match yields v = 43.
	{"error-ok-path",
		`trait Error { function code(self: Self): i32; } struct NotFound { n: i32 } impl Error for NotFound { function code(self: Self): i32 { return self.n; } } function find(ok: boolean): Result[i32, NotFound] { if (ok) { return Ok(42); } return Err(NotFound { n: 7 }); } function handler(ok: boolean): Result[i32, dyn Error] { var v: i32 = find(ok)?; return Ok(v + 1); } function main(): i32 { var result: i32 = 0; match (handler(true)) { Ok(v) => { result = v; }, Err(e) => { result = e.code(); } } return result; }`,
		43},

	// IIFE (value-position) match with a dyn Error payload: both the Ok arm
	// and the Err arm yield i32.  iife_payload_bindable must admit the dyn
	// payload.  handler(false) → Err(NotFound{n:7}) → e.code() = 7.
	{"iife-err-arm-dyn-dispatch",
		`trait Error { function code(self: Self): i32; } struct NotFound { n: i32 } impl Error for NotFound { function code(self: Self): i32 { return self.n; } } function find(ok: boolean): Result[i32, NotFound] { if (ok) { return Ok(42); } return Err(NotFound { n: 7 }); } function handler(ok: boolean): Result[i32, dyn Error] { var v: i32 = find(ok)?; return Ok(v + 1); } function main(): i32 { var a: i32 = match (handler(true)) { Ok(v) => v, Err(e) => e.code() }; var b: i32 = match (handler(false)) { Ok(v) => v, Err(e) => e.code() }; return a + b; }`,
		50}, // 43 + 7

	// Two Error impls in scope; dispatch routes to the right concrete method.
	// find_nf returns NotFound{n:3}; find_pe returns PermError{code:5}.
	// handler_nf → dyn Error(NotFound), handler_pe → dyn Error(PermError).
	// e.code() for NotFound = 3, for PermError = 5; sum = 8.
	{"two-impls-dispatch",
		`trait Error { function code(self: Self): i32; } struct NotFound { n: i32 } struct PermError { c: i32 } impl Error for NotFound { function code(self: Self): i32 { return self.n; } } impl Error for PermError { function code(self: Self): i32 { return self.c; } } function find_nf(): Result[i32, NotFound] { return Err(NotFound { n: 3 }); } function find_pe(): Result[i32, PermError] { return Err(PermError { c: 5 }); } function run_nf(): Result[i32, dyn Error] { var v: i32 = find_nf()?; return Ok(v); } function run_pe(): Result[i32, dyn Error] { var v: i32 = find_pe()?; return Ok(v); } function main(): i32 { var a: i32 = 0; var b: i32 = 0; match (run_nf()) { Ok(v) => { a = v; }, Err(e) => { a = e.code(); } } match (run_pe()) { Ok(v) => { b = v; }, Err(e) => { b = e.code(); } } return a + b; }`,
		8},

	// Error trait with message() → string: bind via explicit `string`
	// annotation so the slot is string-typed; then return the length.
	// NotFound.message() = "not found" (9 chars).
	{"error-message-string",
		`trait Error { function message(self: Self): string; } struct NotFound { n: i32 } impl Error for NotFound { function message(self: Self): string { return "not found"; } } function find(ok: boolean): Result[i32, NotFound] { if (ok) { return Ok(1); } return Err(NotFound { n: 0 }); } function handler(ok: boolean): Result[i32, dyn Error] { var v: i32 = find(ok)?; return Ok(v); } function main(): i32 { var msg_len: i32 = 0; match (handler(false)) { Ok(v) => { msg_len = 0; }, Err(e) => { var m: string = e.message(); msg_len = m.len(); } } return msg_len; }`,
		9},

	// Combined: Error trait has both code() and message(); use both in Err arm.
	// code() = 4, message = "oops" (4 chars); result = 4 + 4 = 8.
	{"error-code-and-message",
		`trait Error { function code(self: Self): i32; function message(self: Self): string; } struct Fail { c: i32 } impl Error for Fail { function code(self: Self): i32 { return self.c; } function message(self: Self): string { return "oops"; } } function run(): Result[i32, Fail] { return Err(Fail { c: 4 }); } function handler(): Result[i32, dyn Error] { var v: i32 = run()?; return Ok(v); } function main(): i32 { var result: i32 = 0; match (handler()) { Ok(v) => { result = v; }, Err(e) => { var m: string = e.message(); result = e.code() + m.len(); } } return result; }`,
		8},
}

// TestSelfHostErrorTraitIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code and the leak census.
func TestSelfHostErrorTraitIR(t *testing.T) {
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
