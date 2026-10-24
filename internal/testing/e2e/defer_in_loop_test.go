package e2e

import "testing"

func TestDeferInLoop(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"while_runs_per_iteration", `function f(a: Cell[i32]): i32 {
    let i: i32 = 0;
    while (i < 3) {
        defer a.set(a.get() + 1);
        i = i + 1;
    }
    return a.get();
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let inside: i32 = f(a);
    return inside * 10 + a.get();
}`, 33},
		{"for_in_runs_per_iteration", `function f(a: Cell[i32]): i32 {
    for i in 0..3 {
        defer a.set(a.get() + 1);
    }
    return a.get();
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let inside: i32 = f(a);
    return inside * 10 + a.get();
}`, 33},
		{"reads_this_iterations_local", `function f(a: Cell[i32]): i32 {
    for i in 0..4 {
        let k: i32 = i * 3;
        defer a.set(a.get() + k);
    }
    return a.get();
}
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 18},
		{"visible_to_the_next_iteration", `function f(a: Cell[i32]): i32 {
    let i: i32 = 0;
    let seen: i32 = 0;
    while (i < 3) {
        if (i == 2) { seen = a.get(); }
        defer a.set(a.get() + 1);
        i = i + 1;
    }
    return seen * 10 + a.get();
}
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 23},
		{"break_runs_the_pending_action", `function f(a: Cell[i32]): i32 {
    let i: i32 = 0;
    while (i < 5) {
        defer a.set(a.get() + 1);
        if (i == 2) { break; }
        i = i + 1;
    }
    return a.get();
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let inside: i32 = f(a);
    return inside * 10 + a.get();
}`, 33},
		{"continue_runs_the_pending_action", `function f(a: Cell[i32]): i32 {
    let i: i32 = 0;
    let t: i32 = 0;
    while (i < 4) {
        defer a.set(a.get() + 1);
        i = i + 1;
        if (i == 2) { continue; }
        t = t + 1;
    }
    return t * 10 + a.get();
}
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 34},
		{"labelled_break_unwinds_inner_first", `function f(a: Cell[i32]): i32 {
    let i: i32 = 0;
    outer: while (i < 3) {
        defer a.set(a.get() * 4 + 2);
        let j: i32 = 0;
        while (j < 3) {
            defer a.set(a.get() * 4 + 1);
            if (j == 1) { break outer; }
            j = j + 1;
        }
        i = i + 1;
    }
    return a.get();
}
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 22},
		{"return_from_loop_runs_current_iteration_once", `function f(a: Cell[i32]): i32 {
    let i: i32 = 0;
    defer a.set(a.get() * 4 + 3);
    while (i < 5) {
        defer a.set(a.get() * 4 + 1);
        if (i == 2) { return 7; }
        i = i + 1;
    }
    return 0;
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    if (f(a) != 7) { return 98; }
    return a.get();
}`, 87},
		{"errdefer_in_loop_fires_for_the_failing_iteration", `function f(a: Cell[i32], n: i32): Result[i32, i32] {
    let i: i32 = 0;
    while (i < n) {
        errdefer a.set(a.get() * 10 + 9);
        defer a.set(a.get() + 1);
        if (i == 2) { return Err(1); }
        i = i + 1;
    }
    return Ok(i);
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let b: Cell[i32] = cell_new(0);
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
		{"try_in_loop_runs_current_iteration", `function step(x: i32): Result[i32, i32] {
    if (x == 2) { return Err(9); }
    return Ok(x);
}
function f(a: Cell[i32], n: i32): Result[i32, i32] {
    let i: i32 = 0;
    while (i < n) {
        defer a.set(a.get() + 1);
        let v: i32 = step(i)?;
        i = i + 1;
    }
    return Ok(i);
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    match (f(a, 5)) {
        Ok(v) => { return 92; },
        Err(e) => { if (e != 9) { return 91; } }
    }
    return a.get();
}`, 3},
		{"loop_body_never_entered", `function f(a: Cell[i32]): i32 {
    let i: i32 = 5;
    while (i < 3) {
        defer a.set(7);
        i = i + 1;
    }
    return 4;
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let r: i32 = f(a);
    return a.get() * 10 + r;
}`, 4},
		{"nested_loop_runs_per_inner_iteration", `function f(a: Cell[i32]): i32 {
    let i: i32 = 0;
    while (i < 2) {
        let j: i32 = 0;
        while (j < 3) {
            defer a.set(a.get() + 1);
            j = j + 1;
        }
        i = i + 1;
    }
    return a.get();
}
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 6},
		{"unconditional_loop_per_iteration", `function f(a: Cell[i32]): i32 {
    let i: i32 = 0;
    loop {
        defer a.set(a.get() + 1);
        i = i + 1;
        if (i == 3) { break; }
    }
    return a.get();
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let inside: i32 = f(a);
    return inside * 10 + a.get();
}`, 33},
		{"match_arm_in_loop", `function f(a: Cell[i32]): i32 {
    let i: i32 = 0;
    while (i < 3) {
        let o: Option[i32] = if (i == 1) { None } else { Some(i) };
        match (o) {
            Some(v) => { defer a.set(a.get() * 10 + v + 1); },
            None => { }
        }
        i = i + 1;
    }
    return a.get();
}
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 13},
		{"labelled_continue_ends_both_iterations", `function f(a: Cell[i32]): i32 {
    let i: i32 = 0;
    outer: while (i < 2) {
        defer a.set(a.get() * 3 + 2);
        i = i + 1;
        let j: i32 = 0;
        while (j < 3) {
            defer a.set(a.get() * 3 + 1);
            if (j == 0) { continue outer; }
            j = j + 1;
        }
    }
    return a.get();
}
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 50},
		{"map_for_in_per_entry", `import "core/map";
function f(a: Cell[i32]): i32 {
    let m: Map[string, i32] = Map { "a": 1, "b": 2 };
    for (k, v) in m {
        defer a.set(a.get() + v);
    }
    return a.get();
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let inside: i32 = f(a);
    return inside * 10 + a.get();
}`, 33},
		{"lifo_within_one_iteration", `function f(a: Cell[i32]): i32 {
    let i: i32 = 0;
    while (i < 2) {
        defer a.set(a.get() * 3 + 1);
        defer a.set(a.get() * 3 + 2);
        i = i + 1;
    }
    return a.get();
}
function main(): i32 { let a: Cell[i32] = cell_new(0); return f(a); }`, 70},
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
