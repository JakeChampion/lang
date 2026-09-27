package e2eselfhost

import "testing"

// deferInLoopCases pin the self-host to the same defer-in-a-loop semantics the
// interpreter and the native backends implement (#6379, #6836): each execution
// of the statement schedules its own run, and that run happens when the
// iteration that executed it ends — its tail, a `break`, a `continue`, a
// labelled edge out of several bodies at once, or a `return`/`?` leaving the
// function from mid-iteration.
//
// lower_defers_func replays a loop body's actions on every edge out of the body
// and clears the arming flag afterwards, so the count is per iteration and the
// function-exit replay skips an iteration that already ended. These cases are
// the ones that separate that from the previous behaviour (one run, at exit) and
// from its two failure modes: a run that also fires again at function exit, and
// a `break`/`continue` edge that never runs it at all.
//
// Each case encodes its whole contract in main's exit code through a Cell[i32]
// the actions mutate, so one source serves the x86-64 and wasm legs. Values
// stay under 126 — wasmtime rejects an exit status outside [0..126).
var deferInLoopCases = []struct {
	name string
	main string
	want int
}{
	// The count, and that the runs are done by the time the loop ends: 3 inside
	// f and 3 to the caller. One run at function exit reports 0 inside, 1 out.
	{"while_per_iteration", `function f(a: Cell[i32]): i32 { var i: i32 = 0; while (i < 3) { defer a.set(a.get() + 1); i = i + 1; } return a.get(); }
function main(): i32 { var a: Cell[i32] = cell_new(0); var inside: i32 = f(a); return inside * 10 + a.get(); }`, 33},
	{"for_in_per_iteration", `function f(a: Cell[i32]): i32 { for i in 0..3 { defer a.set(a.get() + 1); } return a.get(); }
function main(): i32 { var a: Cell[i32] = cell_new(0); var inside: i32 = f(a); return inside * 10 + a.get(); }`, 33},
	// `break` ends its iteration, so the action runs on that edge and exactly
	// once — a second run at function exit would report 34.
	{"break_edge", `function f(a: Cell[i32]): i32 { var i: i32 = 0; while (i < 5) { defer a.set(a.get() + 1); if (i == 2) { break; } i = i + 1; } return a.get(); }
function main(): i32 { var a: Cell[i32] = cell_new(0); var inside: i32 = f(a); return inside * 10 + a.get(); }`, 33},
	// Same for `continue`: 4 iterations, 4 runs, and the skipped tail leaves t at 3.
	{"continue_edge", `function f(a: Cell[i32]): i32 { var i: i32 = 0; var t: i32 = 0; while (i < 4) { defer a.set(a.get() + 1); i = i + 1; if (i == 2) { continue; } t = t + 1; } return t * 10 + a.get(); }
function main(): i32 { var a: Cell[i32] = cell_new(0); return f(a); }`, 34},
	// A labelled `break` leaves both bodies, innermost first: 0 -> 1 -> 5 -> 22.
	// Outer-first leaves 25; running only the inner body's action leaves 5.
	{"labelled_break_unwinds_inner_first", `function f(a: Cell[i32]): i32 { var i: i32 = 0; outer: while (i < 3) { defer a.set(a.get() * 4 + 2); var j: i32 = 0; while (j < 3) { defer a.set(a.get() * 4 + 1); if (j == 1) { break outer; } j = j + 1; } i = i + 1; } return a.get(); }
function main(): i32 { var a: Cell[i32] = cell_new(0); return f(a); }`, 22},
	// A `return` from mid-iteration: the pending action runs there, LIFO with
	// the function-body defer, and the ended iterations do not run again.
	// 0 -> 1 -> 5 (two ended) -> 21 (the returning one) -> 87 (function-body).
	{"return_mid_iteration", `function f(a: Cell[i32]): i32 { var i: i32 = 0; defer a.set(a.get() * 4 + 3); while (i < 5) { defer a.set(a.get() * 4 + 1); if (i == 2) { return 7; } i = i + 1; } return 0; }
function main(): i32 { var a: Cell[i32] = cell_new(0); if (f(a) != 7) { return 98; } return a.get(); }`, 87},
	// An errdefer belongs to its iteration too: the failing iteration's rollback
	// fires (3 -> 39) and an iteration that ended normally leaves nothing behind.
	{"errdefer_failing_iteration", `function f(a: Cell[i32], n: i32): Result[i32, i32] { var i: i32 = 0; while (i < n) { errdefer a.set(a.get() * 10 + 9); defer a.set(a.get() + 1); if (i == 2) { return Err(1); } i = i + 1; } return Ok(i); }
function main(): i32 { var a: Cell[i32] = cell_new(0); var b: Cell[i32] = cell_new(0); match (f(a, 2)) { Ok(v) => { if (v != 2) { return 97; } }, Err(e) => { return 96; } } if (a.get() != 2) { return 95; } match (f(b, 5)) { Ok(v) => { return 94; }, Err(e) => { if (e != 1) { return 93; } } } return b.get(); }`, 39},
	// A `?` propagating out of the body mid-iteration is an exit too: the
	// current iteration's action runs (3), the ended ones do not re-run.
	{"try_edge_mid_iteration", `function step(x: i32): Result[i32, i32] { if (x == 2) { return Err(9); } return Ok(x); }
function f(a: Cell[i32], n: i32): Result[i32, i32] { var i: i32 = 0; while (i < n) { defer a.set(a.get() + 1); var v: i32 = step(i)?; i = i + 1; } return Ok(i); }
function main(): i32 { var a: Cell[i32] = cell_new(0); match (f(a, 5)) { Ok(v) => { return 92; }, Err(e) => { if (e != 9) { return 91; } } } return a.get(); }`, 3},
	// A defer in a match ARM inside a loop belongs to the same iteration —
	// dl_expand_loop_edges has to reach into the arm bodies: 0 -> 1 -> 13, with
	// i == 1 taking the None arm.
	{"match_arm_in_loop", `function f(a: Cell[i32]): i32 { var i: i32 = 0; while (i < 3) { var o: Option[i32] = if (i == 1) { None } else { Some(i) }; match (o) { Some(v) => { defer a.set(a.get() * 10 + v + 1); }, None => { } } i = i + 1; } return a.get(); }
function main(): i32 { var a: Cell[i32] = cell_new(0); return f(a); }`, 13},
	// The same with a counted payload: the binding the replay reads is a
	// string, bound at the top of f at its zero and replaced in the arm.
	{"match_arm_in_loop_string", `function f(a: Cell[i32]): i32 { var i: i32 = 0; while (i < 3) { var o: Option[string] = if (i == 1) { None } else { Some("ab" + "c") }; match (o) { Some(v) => { defer a.set(a.get() * 10 + v.len()); }, None => { } } i = i + 1; } return a.get(); }
function main(): i32 { var a: Cell[i32] = cell_new(0); return f(a); }`, 33},
	// A labelled `continue` ends the inner iteration AND the outer one, so both
	// run, innermost first: 0 -> 1 -> 5, then 16 -> 50.
	{"labelled_continue_ends_both_iterations", `function f(a: Cell[i32]): i32 { var i: i32 = 0; outer: while (i < 2) { defer a.set(a.get() * 3 + 2); i = i + 1; var j: i32 = 0; while (j < 3) { defer a.set(a.get() * 3 + 1); if (j == 0) { continue outer; } j = j + 1; } } return a.get(); }
function main(): i32 { var a: Cell[i32] = cell_new(0); return f(a); }`, 50},
	// Two defers in one body run LIFO within each iteration: 0 -> 2 -> 7, then
	// 23 -> 70. First-in-first-out would leave 50.
	{"lifo_within_one_iteration", `function f(a: Cell[i32]): i32 { var i: i32 = 0; while (i < 2) { defer a.set(a.get() * 3 + 1); defer a.set(a.get() * 3 + 2); i = i + 1; } return a.get(); }
function main(): i32 { var a: Cell[i32] = cell_new(0); return f(a); }`, 70},
}

// TestSelfHostDeferInLoopIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostDeferInLoopIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range deferInLoopCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, tc.main+"\n", target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
