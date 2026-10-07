package e2ecompiler

import "testing"

// optMakeI64IRCases pin the i64/u64 Option/Result CONSTRUCTION width to the
// self-host IR path on x86-64 + wasm. `return Ok(40)` / `Some(40)` / `Err(40)` in
// a function whose declared payload is i64/u64 must build the 8-byte box (payload
// at offset 8) that every consumer reads via op_opt_payload_w(64, true) — match
// arms and the try-operator. The payload width comes from the expected type (the
// return type, or a let-binding's annotation), not from the ARGUMENT: a bare i32
// literal or i32 arg is widened into the 8-byte construction (op_int_extend).
// Built as a 4-byte payload at offset 4, a width-64 read returns garbage (wasm
// reads 0). Each arm READS the unwrapped payload value through to the exit code,
// so the 8-byte round-trip is exercised; every result is <= 126 (wasmtime
// exit-code truncation, cf. #2908). Oracle-checked against the interpreter.
// Mirrors self_host_nested_array_ir_test.go.
var optMakeI64IRCases = []struct {
	name string
	main string
}{
	// Ok(<i32 literal>) in a Result[i64,_] fn: payload must be 8-byte. 40/8 = 5.
	{"ok-i64-literal", `function g(): Result[i64, i32] { return Ok(40); } function main(): i32 { match (g()) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } }`},
	// Some(<i32 literal>) in an Option[i64] fn. 40/8 = 5.
	{"some-i64-literal", `function g(): Option[i64] { return Some(40); } function main(): i32 { match (g()) { Some(v) => { return (v / 8) as i32; }, None => { return 0; } } }`},
	// Err(<i32 literal>) with an i64 error payload (Result[i32, i64]). 40/8 = 5.
	{"err-i64-literal", `function g(): Result[i32, i64] { return Err(40); } function main(): i32 { match (g()) { Ok(v) => { return v; }, Err(e) => { return (e / 8) as i32; } } }`},
	// Ok(<i32 variable>) in a Result[i64,_] fn: the i32 value is widened to 8 bytes
	// (op_int_extend), not stored as a 4-byte payload. 40/8 = 5.
	{"ok-i64-i32var", `function g(n: i32): Result[i64, i32] { return Ok(n); } function main(): i32 { match (g(40)) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } }`},
	// u64 payload — the unsigned 8-byte construction. 99/9 = 11.
	{"ok-u64-literal", `function g(): Result[u64, i32] { return Ok(99); } function main(): i32 { match (g()) { Ok(v) => { return (v / 9) as i32; }, Err(e) => { return e; } } }`},
	// The unwrapped i64 payload flows through the try-operator (offset-8 read) and is
	// re-wrapped (offset-8 construction) before the final match reads it. 40/8 = 5.
	{"try-roundtrip-i64", `function g(): Result[i64, i32] { return Ok(40); } function f(): Result[i64, i32] { let x: i64 = g()?; return Ok(x); } function main(): i32 { match (f()) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } }`},

	// Annotated let-binding construction: `let r: Result[i64,_] = Ok(40)` /
	// `let o: Option[i64] = Some(40)` with a bare i32 literal must build the 8-byte
	// box AND record the annotation as the slot's opt_type so the later `match` reads
	// offset-8 — both sides must agree or the read truncates. (let-ok-i64-literal was
	// a silent miscompile: wasm read 0. let-some-i64-literal was only accidentally
	// correct — i32 construct + i32 read both at offset 4 — and truncated large i64s.)
	{"let-ok-i64-literal", `function main(): i32 { let r: Result[i64, i32] = Ok(40); match (r) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } }`},
	{"let-some-i64-literal", `function main(): i32 { let o: Option[i64] = Some(40); match (o) { Some(v) => { return (v / 8) as i32; }, None => { return 0; } } }`},
	{"let-err-i64-literal", `function main(): i32 { let r: Result[i32, i64] = Err(40); match (r) { Ok(v) => { return v; }, Err(e) => { return (e / 8) as i32; } } }`},
	// i32-variable arg in an annotated let (Result only — the Option[i64] = Some(i32var)
	// form is a checker type error). The i32 value is widened to 8 bytes (op_int_extend).
	{"let-ok-i64-i32var", `function g(n: i32): i32 { let r: Result[i64, i32] = Ok(n); match (r) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } } function main(): i32 { return g(40); }`},
	{"let-some-u64-literal", `function main(): i32 { let o: Option[u64] = Some(99); match (o) { Some(v) => { return (v / 9) as i32; }, None => { return 0; } } }`},
	// LARGE i64 values: a genuine 8-byte round-trip, NOT the accidental i32
	// cancellation (5000000000 truncated to i32 would not divide to 5). Proves the
	// match-read uses offset-8 for both Option and Result locals. 5e9 / 1e9 = 5.
	{"let-some-i64-large", `function main(): i32 { let o: Option[i64] = Some(5000000000); match (o) { Some(v) => { return (v / 1000000000) as i32; }, None => { return 0; } } }`},
	{"let-ok-i64-large", `function main(): i32 { let r: Result[i64, i32] = Ok(5000000000); match (r) { Ok(v) => { return (v / 1000000000) as i32; }, Err(e) => { return e; } } }`},
}

// TestSelfHostOptMakeI64IR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostOptMakeI64IR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range optMakeI64IRCases {
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
