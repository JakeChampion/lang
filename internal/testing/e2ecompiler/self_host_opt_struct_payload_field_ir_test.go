package e2ecompiler

import "testing"

var optStructPayloadFieldIRCases = []struct {
	name string
	main string
}{
	{"opt-struct-some", `struct Inner { a: i32 } struct Outer { v: i32, opt: Option[Inner] } function main(): i32 { let o = Outer { v: 5, opt: Some(Inner { a: 10 }) }; match (o.opt) { Some(n) => { return o.v + n.a; }, None => { return o.v; } } }`},
	{"opt-struct-none", `struct Inner { a: i32 } struct Outer { v: i32, opt: Option[Inner] } function main(): i32 { let o = Outer { v: 5, opt: None }; match (o.opt) { Some(n) => { return o.v + n.a; }, None => { return o.v; } } }`},
	{"result-struct-ok", `struct Inner { a: i32 } struct Outer { res: Result[Inner, string] } function main(): i32 { let o = Outer { res: Ok(Inner { a: 33 }) }; match (o.res) { Ok(n) => { return n.a; }, Err(e) => { return 0; } } }`},
	{"opt-struct-fn-param", `struct Inner { a: i32 } struct Outer { v: i32, opt: Option[Inner] } function total(o: Outer): i32 { match (o.opt) { Some(n) => { return o.v + n.a; }, None => { return o.v; } } } function main(): i32 { return total(Outer { v: 100, opt: Some(Inner { a: 23 }) }); }`},
	{"opt-struct-field-to-local", `struct Inner { a: i32 } struct Outer { opt: Option[Inner] } function main(): i32 { let o = Outer { opt: Some(Inner { a: 42 }) }; let x: Option[Inner] = o.opt; match (x) { Some(n) => { return n.a; }, None => { return 0; } } }`},
	{"opt-enum-some", `enum Color { Red, Blue } struct Box { c: Option[Color] } function main(): i32 { let b = Box { c: Some(Blue) }; match (b.c) { Some(x) => { match (x) { Red => { return 1; }, Blue => { return 2; } } }, None => { return 0; } } }`},
	{"opt-enum-none", `enum Color { Red, Blue } struct Box { c: Option[Color] } function main(): i32 { let b = Box { c: None }; match (b.c) { Some(x) => { return 2; }, None => { return 0; } } }`},
	{"opt-enum-string-payload", `enum E { Msg(string), Quiet } struct Box { e: Option[E] } function main(): i32 { let b = Box { e: Some(Msg("seven!!")) }; match (b.e) { Some(x) => { match (x) { Msg(m) => { return m.len(); }, Quiet => { return 1; } } }, None => { return 0; } } }`},
	{"opt-enum-field-to-local", `enum Color { Red, Blue } struct Box { n: i32, c: Option[Color] } function main(): i32 { let b = Box { n: 1, c: None }; b = Box { ...b, c: Some(Blue) }; let x: Option[Color] = b.c; match (x) { Some(v) => { match (v) { Red => { return 1; }, Blue => { return 2; } } }, None => { return 0; } } }`},
}

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
