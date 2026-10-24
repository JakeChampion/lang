package e2ecompiler

import "testing"

var deferBlockLocalCases = []struct {
	name string
	main string
	want int
}{
	{"if_block_local", `function f(a: Cell[i32]): i32 { let n: i32 = 1; if (n > 0) { let k: i32 = 7; defer a.set(a.get() + k); n = n + 1; } return n; }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = f(a); return a.get() * 10 + r; }`, 72},
	{"while_block_local", `function f(a: Cell[i32]): i32 { let i: i32 = 0; while (i < 3) { let k: i32 = i + 1; defer a.set(a.get() + k); i = i + 1; } return i; }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = f(a); return a.get() * 10 + r; }`, 63},
	{"for_block_local", `function f(a: Cell[i32]): i32 { for i in 0..3 { let k: i32 = i * 2; defer a.set(a.get() + k); } return 9; }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = f(a); return a.get() * 10 + r; }`, 69},
	{"match_arm_block_local", `function f(a: Cell[i32], o: Option[i32]): i32 { match (o) { Some(v) => { let k: i32 = v + 1; defer a.set(a.get() + k); }, None => {} } return 1; }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = f(a, Some(4)); return a.get() * 10 + r; }`, 51},
	{"sibling_blocks_same_name", `function f(a: Cell[i32]): i32 { let n: i32 = 1; if (n > 0) { let k: i32 = 3; defer a.set(a.get() * 4 + k); } if (n > 0) { let k: i32 = 5; defer a.set(a.get() * 4 + k); } return 0; }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = f(a); return a.get() * 4 + r; }`, 92},
	{"block_never_entered", `function f(a: Cell[i32], c: boolean): i32 { if (c) { let k: i32 = 7; defer a.set(a.get() + k); } return 1; }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = f(a, false); return a.get() * 10 + r; }`, 1},
	{"nested_defer_no_local", `function f(a: Cell[i32]): i32 { let n: i32 = 1; if (n > 0) { defer a.set(4); } return 1; }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = f(a); return a.get() * 10 + r; }`, 41},
	{"shadows_outer_local", `function f(a: Cell[i32]): i32 { let k: i32 = 1; if (k > 0) { let k: i32 = 8; defer a.set(a.get() + k); } return k; }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = f(a); return a.get() * 10 + r; }`, 81},
	{"reads_value_at_exit", `function f(a: Cell[i32]): i32 { let n: i32 = 1; if (n > 0) { let k: i32 = 2; defer a.set(a.get() + k); k = 9; } return 1; }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = f(a); return a.get() * 10 + r; }`, 91},
	{"return_in_later_block", `function f(a: Cell[i32], c: boolean): i32 { let n: i32 = 1; if (n > 0) { let k: i32 = 6; defer a.set(a.get() + k); } if (c) { return 3; } return 4; }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = f(a, true); return a.get() * 10 + r; }`, 63},
	{"inner_defer_outer_block_local", `function f(a: Cell[i32]): i32 { let n: i32 = 1; if (n > 0) { let k: i32 = 5; if (n > 0) { defer a.set(a.get() + k); } } return 1; }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = f(a); return a.get() * 10 + r; }`, 51},
	{"string_block_local", `function f(a: Cell[i32], s: string): i32 { if (s.len() > 0) { let m: string = s + "!"; defer a.set(a.get() + m.len()); } return 1; }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = f(a, "abc"); return a.get() * 10 + r; }`, 41},
	{"try_edge_after_block", `function g(x: i32): Option[i32] { if (x < 0) { return None; } return Some(x); }
function f(a: Cell[i32], x: i32): Option[i32] { let n: i32 = 1; if (n > 0) { let k: i32 = 7; defer a.set(a.get() + k); } let v: i32 = g(x)?; return Some(v + 1); }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = 0; match (f(a, 0 - 5)) { Some(v) => { r = v; }, None => { r = 9; } } return a.get() * 10 + r; }`, 79},
	{"try_edge_inside_block", `function g(x: i32): Option[i32] { if (x < 0) { return None; } return Some(x); }
function f(a: Cell[i32], x: i32): Option[i32] { let n: i32 = 1; if (n > 0) { let k: i32 = 4; defer a.set(a.get() + k); let v: i32 = g(x)?; return Some(v + n); } return Some(0); }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = 0; match (f(a, 0 - 5)) { Some(v) => { r = v; }, None => { r = 9; } } return a.get() * 10 + r; }`, 49},
	{"errdefer_block_local", `function f(a: Cell[i32], x: i32): Result[i32, i32] { let n: i32 = 1; if (n > 0) { let k: i32 = 6; errdefer a.set(a.get() + k); } if (x < 0) { return Err(1); } return Ok(x); }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = 0; match (f(a, 0 - 1)) { Ok(v) => { r = v; }, Err(e) => { r = e; } } return a.get() * 10 + r; }`, 61},
	{"errdefer_block_local_ok_path", `function f(a: Cell[i32], x: i32): Result[i32, i32] { let n: i32 = 1; if (n > 0) { let k: i32 = 6; errdefer a.set(a.get() + k); } if (x < 0) { return Err(1); } return Ok(x); }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = 0; match (f(a, 3)) { Ok(v) => { r = v; }, Err(e) => { r = e; } } return a.get() * 10 + r; }`, 3},
	{"break_before_defer_block_local", `function f(a: Cell[i32]): i32 { let i: i32 = 0; while (true) { i = i + 1; if (i >= 3) { break; } let k: i32 = i * 2; defer a.set(a.get() + k); } return 9; }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = f(a); return a.get() * 10 + r; }`, 69},
	{"continue_before_defer_block_local", `function f(a: Cell[i32]): i32 { let i: i32 = 0; while (i < 4) { i = i + 1; if (i == 2) { continue; } let k: i32 = i; defer a.set(a.get() * 2 + k); } return a.get(); }
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 14},
	{"labelled_break_before_defer", `function f(a: Cell[i32]): i32 { let i: i32 = 0; outer: while (i < 3) { let j: i32 = 0; while (j < 2) { if (i == 1) { break outer; } j = j + 1; } let k: i32 = i + 3; defer a.set(a.get() * 4 + k); i = i + 1; } return 7; }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = f(a); return a.get() * 10 + r; }`, 37},
	{"for_break_after_defer_block_local", `function f(a: Cell[i32]): i32 { for i in 0..5 { let k: i32 = i + 1; defer a.set(a.get() * 3 + k); if (i == 1) { break; } } return 8; }
function main(): i32 { let a: Cell[i32] = cell_new(0); let r: i32 = f(a); return a.get() * 10 + r; }`, 58},
}

func TestSelfHostDeferBlockLocalIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range deferBlockLocalCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.main+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
