package e2ecompiler

import "testing"

var tryF64IRCases = []struct {
	name string
	src  string
	want int
}{
	{"result_f64", `function mk(n: i32): Result[f64, string] { if (n > 0) { return Ok(3.5); } return Err("no"); }
function run(n: i32): Result[i32, string] { let x: f64 = mk(n)?; return Ok((x * 2.0) as i32); }
function main(): i32 { match (run(1)) { Ok(v) => { return v; }, Err(_) => { return 99; } } }`, 7},
	{"option_f64", `function find(n: i32): Option[f64] { if (n > 0) { return Some(4.5); } return None; }
function run(n: i32): Option[i32] { let x: f64 = find(n)?; return Some(x as i32); }
function main(): i32 { match (run(1)) { Some(v) => { return v; }, None => { return 0; } } }`, 4},
	{"result_f64_err", `function mk(n: i32): Result[f64, string] { if (n > 0) { return Ok(3.5); } return Err("no"); }
function run(n: i32): Result[i32, string] { let x: f64 = mk(n)?; return Ok((x * 2.0) as i32); }
function main(): i32 { match (run(0)) { Ok(v) => { return v; }, Err(_) => { return 42; } } }`, 42},
	{"option_f64_none", `function find(n: i32): Option[f64] { if (n > 0) { return Some(4.5); } return None; }
function run(n: i32): Option[i32] { let x: f64 = find(n)?; return Some(x as i32); }
function main(): i32 { match (run(0)) { Some(v) => { return v; }, None => { return 9; } } }`, 9},
}

func TestSelfHostTryF64IR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range tryF64IRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
