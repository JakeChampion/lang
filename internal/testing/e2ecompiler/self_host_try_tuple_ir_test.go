package e2ecompiler

import "testing"

var tryTupleIRCases = []struct {
	name string
	src  string
	want int
}{
	{"tuple_first", `function mk(n: i32): Result[(i32, string), string] { if (n > 0) { return Ok((7, "hi")); } return Err("no"); }
function run(n: i32): Result[i32, string] { let x: (i32, string) = mk(n)?; return Ok(x.0); }
function main(): i32 { match (run(1)) { Ok(v) => { return v; }, Err(_) => { return 99; } } }`, 7},
	{"tuple_sum", `function find(n: i32): Option[(i32, i32)] { if (n > 0) { return Some((4, 5)); } return None; }
function run(n: i32): Option[i32] { let p: (i32, i32) = find(n)?; return Some(p.0 + p.1); }
function main(): i32 { match (run(1)) { Some(v) => { return v; }, None => { return 0; } } }`, 9},
	{"tuple_err", `function mk(n: i32): Result[(i32, string), string] { if (n > 0) { return Ok((7, "hi")); } return Err("no"); }
function run(n: i32): Result[i32, string] { let x: (i32, string) = mk(n)?; return Ok(x.0); }
function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(_) => { return 42; } } }`, 42},
	{"tuple_none", `function find(n: i32): Option[(i32, i32)] { if (n > 0) { return Some((4, 5)); } return None; }
function run(n: i32): Option[i32] { let p: (i32, i32) = find(n)?; return Some(p.0 + p.1); }
function main(): i32 { match (run(0)) { Some(v) => { return v; }, None => { return 8; } } }`, 8},
}

func TestSelfHostTryTupleIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range tryTupleIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
