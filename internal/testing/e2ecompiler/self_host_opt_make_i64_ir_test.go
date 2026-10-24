package e2ecompiler

import "testing"

var optMakeI64IRCases = []struct {
	name string
	main string
}{
	{"ok-i64-literal", `function g(): Result[i64, i32] { return Ok(40); } function main(): i32 { match (g()) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } }`},
	{"some-i64-literal", `function g(): Option[i64] { return Some(40); } function main(): i32 { match (g()) { Some(v) => { return (v / 8) as i32; }, None => { return 0; } } }`},
	{"err-i64-literal", `function g(): Result[i32, i64] { return Err(40); } function main(): i32 { match (g()) { Ok(v) => { return v; }, Err(e) => { return (e / 8) as i32; } } }`},
	{"ok-i64-i32var", `function g(n: i32): Result[i64, i32] { return Ok(n); } function main(): i32 { match (g(40)) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } }`},
	{"ok-u64-literal", `function g(): Result[u64, i32] { return Ok(99); } function main(): i32 { match (g()) { Ok(v) => { return (v / 9) as i32; }, Err(e) => { return e; } } }`},
	{"try-roundtrip-i64", `function g(): Result[i64, i32] { return Ok(40); } function f(): Result[i64, i32] { let x: i64 = g()?; return Ok(x); } function main(): i32 { match (f()) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } }`},

	{"let-ok-i64-literal", `function main(): i32 { let r: Result[i64, i32] = Ok(40); match (r) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } }`},
	{"let-some-i64-literal", `function main(): i32 { let o: Option[i64] = Some(40); match (o) { Some(v) => { return (v / 8) as i32; }, None => { return 0; } } }`},
	{"let-err-i64-literal", `function main(): i32 { let r: Result[i32, i64] = Err(40); match (r) { Ok(v) => { return v; }, Err(e) => { return (e / 8) as i32; } } }`},
	{"let-ok-i64-i32var", `function g(n: i32): i32 { let r: Result[i64, i32] = Ok(n); match (r) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } } function main(): i32 { return g(40); }`},
	{"let-some-u64-literal", `function main(): i32 { let o: Option[u64] = Some(99); match (o) { Some(v) => { return (v / 9) as i32; }, None => { return 0; } } }`},
	{"let-some-i64-large", `function main(): i32 { let o: Option[i64] = Some(5000000000); match (o) { Some(v) => { return (v / 1000000000) as i32; }, None => { return 0; } } }`},
	{"let-ok-i64-large", `function main(): i32 { let r: Result[i64, i32] = Ok(5000000000); match (r) { Ok(v) => { return (v / 1000000000) as i32; }, Err(e) => { return e; } } }`},
}

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
