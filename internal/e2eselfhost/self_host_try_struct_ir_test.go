package e2eselfhost

import "testing"

// tryStructIRCases pin the `?` (try) operator on a STRUCT-payload Result/Option
// on the self-host IR path (#3802). `?` was IR-eligible only for scalar payloads
// (i32/boolean/i64/u64); a struct payload bailed the whole module to the legacy
// AST path. lower_try now admits a leaf-safe struct payload — op_opt_payload
// reads the pointer-width struct box exactly as the match-arm Some/Ok binding
// does, and the `var p: P = inner?` binding types p's slot from its annotation,
// so `p.field` resolves. Each case is value-pinned against the native
// interpreter oracle (interp == native).
var tryStructIRCases = []struct {
	name string
	src  string
	want int
}{
	// `?` on Result[Struct, string]: unwrap P, read two fields. 7→P{7,14}→21.
	{"result_struct", `struct P { x: i32, y: i32 }
function mk(n: i32): Result[P, string] { if (n > 0) { return Ok(P { x: n, y: n * 2 }); } return Err("neg"); }
function run(n: i32): Result[i32, string] { var p: P = mk(n)?; return Ok(p.x + p.y); }
function main(): i32 { match (run(7)) { Ok(v) => { return v; }, Err(_) => { return 99; } } }`, 21},
	// `?` on Option[Struct]: unwrap Node, read field + 1. 5→Node{15}→16.
	{"option_struct", `struct Node { val: i32 }
function find(n: i32): Option[Node] { if (n > 0) { return Some(Node { val: n * 3 }); } return None; }
function run(n: i32): Option[i32] { var nd: Node = find(n)?; return Some(nd.val + 1); }
function main(): i32 { match (run(5)) { Some(v) => { return v; }, None => { return 0; } } }`, 16},
	// the failure path of a struct-payload `?`: Err short-circuits, forwarding the
	// source Err box unchanged. run(0) → Err("neg") → the outer match Err arm → 99.
	{"result_struct_err", `struct P { x: i32, y: i32 }
function mk(n: i32): Result[P, string] { if (n > 0) { return Ok(P { x: n, y: n * 2 }); } return Err("neg"); }
function run(n: i32): Result[i32, string] { var p: P = mk(n)?; return Ok(p.x + p.y); }
function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(_) => { return 42; } } }`, 42},
	// the None path of an Option-struct `?`: find(0) → None → short-circuit → 7.
	{"option_struct_none", `struct Node { val: i32 }
function find(n: i32): Option[Node] { if (n > 0) { return Some(Node { val: n * 3 }); } return None; }
function run(n: i32): Option[i32] { var nd: Node = find(n)?; return Some(nd.val + 1); }
function main(): i32 { match (run(0)) { Some(v) => { return v; }, None => { return 7; } } }`, 7},
	// two chained `?` on struct payloads in one function (both must unwrap).
	{"two_chained", `struct P { x: i32 }
function a(n: i32): Result[P, string] { if (n > 0) { return Ok(P { x: n }); } return Err("a"); }
function b(n: i32): Result[P, string] { if (n > 0) { return Ok(P { x: n + 100 }); } return Err("b"); }
function run(n: i32): Result[i32, string] { var p: P = a(n)?; var q: P = b(n)?; return Ok(p.x + q.x); }
function main(): i32 { match (run(5)) { Ok(v) => { return v; }, Err(_) => { return 0; } } }`, 110},
}

// TestSelfHostTryStructIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
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
