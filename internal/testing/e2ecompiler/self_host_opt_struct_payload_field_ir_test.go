package e2ecompiler

import "testing"

// optStructPayloadFieldIRCases pin a struct FIELD whose Option/Result payload is
// itself a STRUCT (`Option[Inner]` / `Result[Inner, …]`) or an enum to the
// self-host IR path on x86-64 + wasm (#2691); scalar-payload Option/Result
// fields are in self_host_opt_struct_field_ir_test.go. Each case is
// oracle-checked against the interpreter and returns <= 126. Mirrors
// self_host_nested_array_ir_test.go.
var optStructPayloadFieldIRCases = []struct {
	name string
	main string
}{
	// Option[Struct] field, Some arm reads the nested struct's field. 5 + 10 = 15.
	{"opt-struct-some", `struct Inner { a: i32 } struct Outer { v: i32, opt: Option[Inner] } function main(): i32 { let o = Outer { v: 5, opt: Some(Inner { a: 10 }) }; match (o.opt) { Some(n) => { return o.v + n.a; }, None => { return o.v; } } }`},
	// Option[Struct] field = None — the None arm. 5.
	{"opt-struct-none", `struct Inner { a: i32 } struct Outer { v: i32, opt: Option[Inner] } function main(): i32 { let o = Outer { v: 5, opt: None }; match (o.opt) { Some(n) => { return o.v + n.a; }, None => { return o.v; } } }`},
	// Result[Struct, string] field, Ok arm. 33.
	{"result-struct-ok", `struct Inner { a: i32 } struct Outer { res: Result[Inner, string] } function main(): i32 { let o = Outer { res: Ok(Inner { a: 33 }) }; match (o.res) { Ok(n) => { return n.a; }, Err(e) => { return 0; } } }`},
	// Outer (with the Option[Struct] field) flows through a by-value function param. 123.
	{"opt-struct-fn-param", `struct Inner { a: i32 } struct Outer { v: i32, opt: Option[Inner] } function total(o: Outer): i32 { match (o.opt) { Some(n) => { return o.v + n.a; }, None => { return o.v; } } } function main(): i32 { return total(Outer { v: 100, opt: Some(Inner { a: 23 }) }); }`},
	// The Option[Struct] field read into a typed local, then matched. 42.
	{"opt-struct-field-to-local", `struct Inner { a: i32 } struct Outer { opt: Option[Inner] } function main(): i32 { let o = Outer { opt: Some(Inner { a: 42 }) }; let x: Option[Inner] = o.opt; match (x) { Some(n) => { return n.a; }, None => { return 0; } } }`},
	// Option[enum] field, Some arm matches the payload's variant. 2.
	{"opt-enum-some", `enum Color { Red, Blue } struct Box { c: Option[Color] } function main(): i32 { let b = Box { c: Some(Blue) }; match (b.c) { Some(x) => { match (x) { Red => { return 1; }, Blue => { return 2; } } }, None => { return 0; } } }`},
	// Option[enum] field = None. 0.
	{"opt-enum-none", `enum Color { Red, Blue } struct Box { c: Option[Color] } function main(): i32 { let b = Box { c: None }; match (b.c) { Some(x) => { return 2; }, None => { return 0; } } }`},
	// Option[enum] field whose variant carries a string payload (the IoError
	// shape a buffered writer holds), read through the payload. 7.
	{"opt-enum-string-payload", `enum E { Msg(string), Quiet } struct Box { e: Option[E] } function main(): i32 { let b = Box { e: Some(Msg("seven!!")) }; match (b.e) { Some(x) => { match (x) { Msg(m) => { return m.len(); }, Quiet => { return 1; } } }, None => { return 0; } } }`},
	// The Option[enum] field read into a typed local, then matched, after the
	// struct was rebuilt with a spread. 2.
	{"opt-enum-field-to-local", `enum Color { Red, Blue } struct Box { n: i32, c: Option[Color] } function main(): i32 { let b = Box { n: 1, c: None }; b = Box { ...b, c: Some(Blue) }; let x: Option[Color] = b.c; match (x) { Some(v) => { match (v) { Red => { return 1; }, Blue => { return 2; } } }, None => { return 0; } } }`},
}

// TestSelfHostOptStructPayloadFieldIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostOptStructPayloadFieldIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range optStructPayloadFieldIRCases {
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
