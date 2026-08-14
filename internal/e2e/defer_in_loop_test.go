package e2e

import "testing"

// TestDeferInLoop pins the semantics of a `defer` inside a loop body, on every
// engine at once (#6379, #6836). Each execution of the statement schedules its
// own run, and that run happens when the iteration that executed it ends — so a
// defer in a loop body fires once per iteration and reads that iteration's
// values, rather than once at function exit reading whatever the locals hold
// then.
//
// The engines disagreed for as long as this went unpinned: the interpreter
// registered one deferred call per EXECUTION (and re-evaluated them all at
// function exit) while every compiled backend armed one flag per defer
// STATEMENT and fired it once. Straight-line code cannot tell the two models
// apart, which is why nothing caught it — so every case here puts the defer in
// a loop, and each one encodes its whole contract in main's exit code (kept
// under 126, which wasmtime rejects) so the same source runs on all four legs.
func TestDeferInLoop(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		// The count, and that the runs are already done when the loop ends: 3
		// iterations, and the cell reads 3 both inside f and to the caller. One
		// run at function exit would report 0 inside and 1 outside.
		{"while_runs_per_iteration", `function f(a: Cell[i32]): i32 {
    var i: i32 = 0;
    while (i < 3) {
        defer a.set(a.get() + 1);
        i = i + 1;
    }
    return a.get();
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var inside: i32 = f(a);
    return inside * 10 + a.get();
}`, 33},
		// The `for … in` form, which desugars to a different loop shape.
		{"for_in_runs_per_iteration", `function f(a: Cell[i32]): i32 {
    for i in 0..3 {
        defer a.set(a.get() + 1);
    }
    return a.get();
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var inside: i32 = f(a);
    return inside * 10 + a.get();
}`, 33},
		// Each run reads ITS iteration's local: 0 + 3 + 6 + 9. Firing once at
		// function exit reads the last k only (9).
		{"reads_this_iterations_local", `function f(a: Cell[i32]): i32 {
    for i in 0..4 {
        var k: i32 = i * 3;
        defer a.set(a.get() + k);
    }
    return a.get();
}
function main(): i32 { var a: Cell[i32] = cell_new(0); return f(a); }`, 18},
		// The timing, observed from inside the loop: on the third iteration the
		// first two iterations' actions have already run.
		{"visible_to_the_next_iteration", `function f(a: Cell[i32]): i32 {
    var i: i32 = 0;
    var seen: i32 = 0;
    while (i < 3) {
        if (i == 2) { seen = a.get(); }
        defer a.set(a.get() + 1);
        i = i + 1;
    }
    return seen * 10 + a.get();
}
function main(): i32 { var a: Cell[i32] = cell_new(0); return f(a); }`, 23},
		// `break` leaves the body, so the iteration's action runs there — and
		// exactly once: a second run at function exit would report 34.
		{"break_runs_the_pending_action", `function f(a: Cell[i32]): i32 {
    var i: i32 = 0;
    while (i < 5) {
        defer a.set(a.get() + 1);
        if (i == 2) { break; }
        i = i + 1;
    }
    return a.get();
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var inside: i32 = f(a);
    return inside * 10 + a.get();
}`, 33},
		// `continue` likewise ends its iteration: 4 iterations, 4 runs, and the
		// skipped tail leaves t at 3.
		{"continue_runs_the_pending_action", `function f(a: Cell[i32]): i32 {
    var i: i32 = 0;
    var t: i32 = 0;
    while (i < 4) {
        defer a.set(a.get() + 1);
        i = i + 1;
        if (i == 2) { continue; }
        t = t + 1;
    }
    return t * 10 + a.get();
}
function main(): i32 { var a: Cell[i32] = cell_new(0); return f(a); }`, 34},
		// A labelled `break` leaves two bodies at once, so both run, innermost
		// first: 0 -> 1 -> 5 (inner, twice) -> 22 (outer). Outer-first would
		// leave 25.
		{"labelled_break_unwinds_inner_first", `function f(a: Cell[i32]): i32 {
    var i: i32 = 0;
    outer: while (i < 3) {
        defer a.set(a.get() * 4 + 2);
        var j: i32 = 0;
        while (j < 3) {
            defer a.set(a.get() * 4 + 1);
            if (j == 1) { break outer; }
            j = j + 1;
        }
        i = i + 1;
    }
    return a.get();
}
function main(): i32 { var a: Cell[i32] = cell_new(0); return f(a); }`, 22},
		// A `return` from mid-iteration is a function exit, so the iteration's
		// pending action runs there, LIFO with the function-level defer, and the
		// iterations that already ended do not run again: 0 -> 1 -> 5 (two ended
		// iterations) -> 21 (the returning one) -> 87 (the function-level defer).
		{"return_from_loop_runs_current_iteration_once", `function f(a: Cell[i32]): i32 {
    var i: i32 = 0;
    defer a.set(a.get() * 4 + 3);
    while (i < 5) {
        defer a.set(a.get() * 4 + 1);
        if (i == 2) { return 7; }
        i = i + 1;
    }
    return 0;
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    if (f(a) != 7) { return 98; }
    return a.get();
}`, 87},
		// An `errdefer` in a loop body belongs to its iteration too: the failing
		// iteration's rollback fires (3 -> 39), and an iteration that ended
		// normally leaves nothing behind to fire on a later failure (a == 2).
		{"errdefer_in_loop_fires_for_the_failing_iteration", `function f(a: Cell[i32], n: i32): Result[i32, i32] {
    var i: i32 = 0;
    while (i < n) {
        errdefer a.set(a.get() * 10 + 9);
        defer a.set(a.get() + 1);
        if (i == 2) { return Err(1); }
        i = i + 1;
    }
    return Ok(i);
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var b: Cell[i32] = cell_new(0);
    match (f(a, 2)) {
        Ok(v) => { if (v != 2) { return 97; } },
        Err(e) => { return 96; }
    }
    if (a.get() != 2) { return 95; }
    match (f(b, 5)) {
        Ok(v) => { return 94; },
        Err(e) => { if (e != 1) { return 93; } }
    }
    return b.get();
}`, 39},
		// A `?` propagating out of the body mid-iteration is an exit too: the
		// current iteration's action runs (a == 3), the ended ones do not re-run.
		{"try_in_loop_runs_current_iteration", `function step(x: i32): Result[i32, i32] {
    if (x == 2) { return Err(9); }
    return Ok(x);
}
function f(a: Cell[i32], n: i32): Result[i32, i32] {
    var i: i32 = 0;
    while (i < n) {
        defer a.set(a.get() + 1);
        var v: i32 = step(i)?;
        i = i + 1;
    }
    return Ok(i);
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    match (f(a, 5)) {
        Ok(v) => { return 92; },
        Err(e) => { if (e != 9) { return 91; } }
    }
    return a.get();
}`, 3},
		// A loop that never runs its body never registers anything.
		{"loop_body_never_entered", `function f(a: Cell[i32]): i32 {
    var i: i32 = 5;
    while (i < 3) {
        defer a.set(7);
        i = i + 1;
    }
    return 4;
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var r: i32 = f(a);
    return a.get() * 10 + r;
}`, 4},
		// A nested loop's body is its own scope: 2 x 3 iterations, 6 runs.
		{"nested_loop_runs_per_inner_iteration", `function f(a: Cell[i32]): i32 {
    var i: i32 = 0;
    while (i < 2) {
        var j: i32 = 0;
        while (j < 3) {
            defer a.set(a.get() + 1);
            j = j + 1;
        }
        i = i + 1;
    }
    return a.get();
}
function main(): i32 { var a: Cell[i32] = cell_new(0); return f(a); }`, 6},
		// Two defers in one body run LIFO within each iteration: 0 -> 2 -> 7,
		// then 23 -> 70. First-in-first-out would leave 50.
		{"lifo_within_one_iteration", `function f(a: Cell[i32]): i32 {
    var i: i32 = 0;
    while (i < 2) {
        defer a.set(a.get() * 3 + 1);
        defer a.set(a.get() * 3 + 2);
        i = i + 1;
    }
    return a.get();
}
function main(): i32 { var a: Cell[i32] = cell_new(0); return f(a); }`, 70},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if code := runInterpExit(t, c.src); code != c.want {
				t.Errorf("interp exit = %d, want %d", code, c.want)
			}
			t.Run("wasm32-wasi", func(t *testing.T) {
				if code := compileAndRunWasmbinMain(t, c.src); code != c.want {
					t.Errorf("wasm exit = %d, want %d", code, c.want)
				}
			})
			t.Run("arm64-linux", func(t *testing.T) {
				if _, code := compileAndRunArm64(t, c.src); code != c.want {
					t.Errorf("arm64 exit = %d, want %d", code, c.want)
				}
			})
			t.Run("x86_64", func(t *testing.T) {
				if _, code := compileAndRunX86_64(t, c.src); code != c.want {
					t.Errorf("x86_64 exit = %d, want %d", code, c.want)
				}
			})
		})
	}
}
