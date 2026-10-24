package e2ecompiler

import "testing"

var tryStrEnumIRCases = []struct {
	name string
	src  string
	want int
}{
	{"result_string", `function mk(n: i32): Result[string, string] { if (n > 0) { return Ok("yes"); } return Err("no"); }
function run(n: i32): Result[i32, string] { let s: string = mk(n)?; return Ok(s.len()); }
function main(): i32 { match (run(1)) { Ok(v) => { return v; }, Err(_) => { return 99; } } }`, 3},
	{"option_string", `function find(n: i32): Option[string] { if (n > 0) { return Some("abcd"); } return None; }
function run(n: i32): Option[i32] { let s: string = find(n)?; return Some(s.len()); }
function main(): i32 { match (run(1)) { Some(v) => { return v; }, None => { return 0; } } }`, 4},
	{"result_enum", `enum Cmd { Add(i32), Sub(i32) }
function mk(n: i32): Result[Cmd, string] { if (n > 0) { return Ok(Add(n)); } return Err("x"); }
function ev(c: Cmd): i32 { match (c) { Add(v) => { return v; }, Sub(v) => { return 0 - v; } } }
function run(n: i32): Result[i32, string] { let c: Cmd = mk(n)?; return Ok(ev(c)); }
function main(): i32 { match (run(8)) { Ok(v) => { return v; }, Err(_) => { return 99; } } }`, 8},
	{"result_string_err", `function mk(n: i32): Result[string, string] { if (n > 0) { return Ok("yes"); } return Err("no"); }
function run(n: i32): Result[i32, string] { let s: string = mk(n)?; return Ok(s.len()); }
function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(_) => { return 42; } } }`, 42},
	{"option_enum_none", `enum Cmd { Add(i32), Sub(i32) }
function find(n: i32): Option[Cmd] { if (n > 0) { return Some(Add(n)); } return None; }
function ev(c: Cmd): i32 { match (c) { Add(v) => { return v; }, Sub(v) => { return 0 - v; } } }
function run(n: i32): Option[i32] { let c: Cmd = find(n)?; return Some(ev(c)); }
function main(): i32 { match (run(0)) { Some(v) => { return v; }, None => { return 7; } } }`, 7},
}

func TestSelfHostTryStrEnumIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range tryStrEnumIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
