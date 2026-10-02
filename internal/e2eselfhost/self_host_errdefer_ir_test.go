package e2eselfhost

import "testing"

// TestSelfHostErrDeferIR exercises `errdefer` through the self-hosted CLI for
// x86-64. errdefer cleanup fires only on an error exit: the explicit
// `return None` / `return Err(...)` form. Side effects are observed through a
// Cell[i32], so the whole contract is encoded in the process exit code.
func TestSelfHostErrDeferIR(t *testing.T) {
	cli := newStrictCLI(t)
	cases := []struct {
		name string
		src  string
		want int
	}{
		// Result: errdefer fires on `return Err`, not on `return Ok`.
		{"success_no_fire", `function f(out: Cell[i32], x: i32): Result[i32, i32] { errdefer out.set(9); if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { let a: Cell[i32] = cell_new(0); match (f(a, 5)) { Ok(v) => {}, Err(e) => {} } return a.get(); }`, 0},
		{"err_return_fires", `function f(out: Cell[i32], x: i32): Result[i32, i32] { errdefer out.set(9); if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { let a: Cell[i32] = cell_new(0); match (f(a, -1)) { Ok(v) => {}, Err(e) => {} } return a.get(); }`, 9},
		// Option: fires on `return None`, not on `return Some`.
		{"option_none_fires", `function opt(out: Cell[i32], x: i32): Option[i32] { errdefer out.set(5); if (x < 0) { return None; } return Some(x); } function main(): i32 { let a: Cell[i32] = cell_new(0); match (opt(a, -1)) { Some(v) => {}, None => {} } return a.get(); }`, 5},
		{"option_some_no_fire", `function opt(out: Cell[i32], x: i32): Option[i32] { errdefer out.set(5); if (x < 0) { return None; } return Some(x); } function main(): i32 { let a: Cell[i32] = cell_new(0); match (opt(a, 7)) { Some(v) => {}, None => {} } return a.get(); }`, 0},
		// defer runs on every exit; errdefer only on the error exit, after the
		// defer. Success: 0 +1 (defer) = 1. Error: 0 +1 (defer) +10 (errdefer) = 11.
		{"defer_and_errdefer_success", `function h(out: Cell[i32], x: i32): Result[i32, i32] { defer out.set(out.get() + 1); errdefer out.set(out.get() + 10); if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { let a: Cell[i32] = cell_new(0); match (h(a, 5)) { Ok(v) => {}, Err(e) => {} } return a.get(); }`, 1},
		{"defer_and_errdefer_error", `function h(out: Cell[i32], x: i32): Result[i32, i32] { defer out.set(out.get() + 1); errdefer out.set(out.get() + 10); if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { let a: Cell[i32] = cell_new(0); match (h(a, -1)) { Ok(v) => {}, Err(e) => {} } return a.get(); }`, 11},
		// Conditionally-reached errdefer: only fires when its statement ran.
		{"conditional_registered_fires", `function cond(out: Cell[i32], reg: boolean, x: i32): Result[i32, i32] { if (reg) { errdefer out.set(7); } if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { let a: Cell[i32] = cell_new(0); match (cond(a, true, -1)) { Ok(v) => {}, Err(e) => {} } return a.get(); }`, 7},
		{"conditional_unregistered_no_fire", `function cond(out: Cell[i32], reg: boolean, x: i32): Result[i32, i32] { if (reg) { errdefer out.set(7); } if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { let a: Cell[i32] = cell_new(0); match (cond(a, false, -1)) { Ok(v) => {}, Err(e) => {} } return a.get(); }`, 0},
		// Two errdefers fire LIFO on error: out = out*10 + id gives 21.
		{"lifo_order", `function m(out: Cell[i32], x: i32): Result[i32, i32] { errdefer out.set(out.get() * 10 + 1); errdefer out.set(out.get() * 10 + 2); if (x < 0) { return Err(1); } return Ok(x); } function main(): i32 { let a: Cell[i32] = cell_new(0); match (m(a, -1)) { Ok(v) => {}, Err(e) => {} } return a.get(); }`, 21},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.want {
				t.Errorf("%s: exit = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
