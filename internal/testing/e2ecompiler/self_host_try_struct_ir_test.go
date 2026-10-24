package e2ecompiler

import "testing"

var tryStructIRCases = []struct {
	name string
	src  string
	want int
}{
	{"result_struct", `struct P { x: i32, y: i32 }
function mk(n: i32): Result[P, string] { if (n > 0) { return Ok(P { x: n, y: n * 2 }); } return Err("neg"); }
function run(n: i32): Result[i32, string] { let p: P = mk(n)?; return Ok(p.x + p.y); }
function main(): i32 { match (run(7)) { Ok(v) => { return v; }, Err(_) => { return 99; } } }`, 21},
	{"option_struct", `struct Node { val: i32 }
function find(n: i32): Option[Node] { if (n > 0) { return Some(Node { val: n * 3 }); } return None; }
function run(n: i32): Option[i32] { let nd: Node = find(n)?; return Some(nd.val + 1); }
function main(): i32 { match (run(5)) { Some(v) => { return v; }, None => { return 0; } } }`, 16},
	{"result_struct_err", `struct P { x: i32, y: i32 }
function mk(n: i32): Result[P, string] { if (n > 0) { return Ok(P { x: n, y: n * 2 }); } return Err("neg"); }
function run(n: i32): Result[i32, string] { let p: P = mk(n)?; return Ok(p.x + p.y); }
function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(_) => { return 42; } } }`, 42},
	{"option_struct_none", `struct Node { val: i32 }
function find(n: i32): Option[Node] { if (n > 0) { return Some(Node { val: n * 3 }); } return None; }
function run(n: i32): Option[i32] { let nd: Node = find(n)?; return Some(nd.val + 1); }
function main(): i32 { match (run(0)) { Some(v) => { return v; }, None => { return 7; } } }`, 7},
	{"two_chained", `struct P { x: i32 }
function a(n: i32): Result[P, string] { if (n > 0) { return Ok(P { x: n }); } return Err("a"); }
function b(n: i32): Result[P, string] { if (n > 0) { return Ok(P { x: n + 100 }); } return Err("b"); }
function run(n: i32): Result[i32, string] { let p: P = a(n)?; let q: P = b(n)?; return Ok(p.x + q.x); }
function main(): i32 { match (run(5)) { Ok(v) => { return v; }, Err(_) => { return 0; } } }`, 110},
}

func TestSelfHostTryStructIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range tryStructIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
