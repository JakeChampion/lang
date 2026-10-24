package e2ecompiler

import "testing"

var tryDeferCases = []struct {
	name string
	src  string
	want int
}{
	{"loop_mid_iteration", `function step(x: i32): Result[i32, i32] { if (x == 2) { return Err(9); } return Ok(x); }
function f(a: Cell[i32], n: i32): Result[i32, i32] { let i: i32 = 0; while (i < n) { defer a.set(a.get() + 1); let v: i32 = step(i)?; i = i + 1; } return Ok(i); }
function main(): i32 { let a: Cell[i32] = cell_new(0); match (f(a, 5)) { Ok(v) => { return 92; }, Err(e) => { if (e != 9) { return 91; } } } return a.get(); }`, 3},
	{"defer_then_errdefer", `function g(x: i32): Option[i32] { if (x < 0) { return None; } return Some(x); }
function f(a: Cell[i32], x: i32): Option[i32] { errdefer a.set(a.get() * 10 + 2); defer a.set(a.get() + 1); let v: i32 = g(x)?; return Some(v); }
function main(): i32 { let a: Cell[i32] = cell_new(0); match (f(a, 0 - 1)) { Some(v) => { return 90; }, None => {} } return a.get(); }`, 12},
	{"defer_after_try_skipped", `function g(x: i32): Option[i32] { if (x < 0) { return None; } return Some(x); }
function f(a: Cell[i32], x: i32): Option[i32] { defer a.set(a.get() + 1); let v: i32 = g(x)?; defer a.set(a.get() + 40); return Some(v); }
function main(): i32 { let a: Cell[i32] = cell_new(0); match (f(a, 0 - 1)) { Some(v) => { return 90; }, None => {} } return a.get(); }`, 1},
	{"success_path", `function g(x: i32): Option[i32] { if (x < 0) { return None; } return Some(x); }
function f(a: Cell[i32], x: i32): Option[i32] { errdefer a.set(99); defer a.set(a.get() + 1); let v: i32 = g(x)?; defer a.set(a.get() + 40); return Some(v); }
function main(): i32 { let a: Cell[i32] = cell_new(0); match (f(a, 5)) { Some(v) => { return a.get() + v; }, None => {} } return 90; }`, 46},
}

func TestSelfHostTryDefer(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range tryDeferCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.src+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
