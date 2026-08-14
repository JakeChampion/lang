package e2e

import "testing"

// TestDeferOverLocalRunsInInterp pins the interpreter to the compiled backends
// for a `defer` whose action names a LOCAL. The interpreter ran every deferred
// expression against the env callFunc holds, which binds parameters only — a
// function's locals live in the child scope execBlock opens for the body, and
// each nested block in another. So the expression failed to resolve its name,
// runDefers dropped the error ("fire and forget"), and the defer silently did
// not run at all. Only a fully literal action ever worked.
//
// A silently skipped defer is the worst shape this can take: the interpreter is
// the differential ORACLE the self-host engines are graded against
// (docs/NATIVE-CONVERGENCE.md §3), so it scored the correct answer wrong.
//
// Each case makes the deferred action's effect part of main's exit code, and
// separately asserts the ORDER — a defer runs at function exit, so an effect
// the caller observes after the call must not be visible before it.
func TestDeferOverLocalRunsInInterp(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		// A function-level local: the plainest form, and it was broken too.
		{"function_level_local", `function f(out: Cell[i32]): i32 {
    var k: i32 = 7;
    defer out.set(out.get() + k);
    return 1;
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var r: i32 = f(a);
    return a.get() * 10 + r;
}`, 71},
		// A local declared inside an `if` body — the #6821 shape, which native
		// codegen has always compiled.
		{"block_scoped_local", `function f(out: Cell[i32]): i32 {
    var n: i32 = 1;
    if (n > 0) {
        var k: i32 = 7;
        defer out.set(out.get() + k);
        n = n + 1;
    }
    return n;
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var r: i32 = f(a);
    return a.get() * 10 + r;
}`, 72},
		// The action reads the local at EXIT, not at the `defer`.
		{"reads_value_at_exit", `function f(out: Cell[i32]): i32 {
    var k: i32 = 2;
    defer out.set(out.get() + k);
    k = 9;
    return 1;
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var r: i32 = f(a);
    return a.get() * 10 + r;
}`, 91},
		// A defer runs at function EXIT: the caller sees 0 before the call
		// returns and 5 after, so a defer hoisted to its own statement position
		// would report 55 rather than 5.
		{"runs_at_function_exit", `function f(out: Cell[i32]): i32 {
    var k: i32 = 5;
    defer out.set(out.get() + k);
    return out.get();
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var before: i32 = f(a);
    return before * 10 + a.get();
}`, 5},
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
