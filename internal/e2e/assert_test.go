package e2e

import "testing"

// TestAssert exercises the `assert(cond)` / `assert(cond, msg)` builtin
// (#4416). It is a parser-level desugar to
// `if (!cond) { eprint("assertion failed[: msg]"); exit(1); }`, so it lowers
// with no dedicated codegen — the whole contract is observable through the
// process exit code: a failing assert aborts with exit 1, a passing one falls
// through to the program's own `return`.
//
// The cases split by observability. A *passing* assert leaves `main`'s return
// value intact, so those run on every backend (interp / arm64 / x86-64 / wasm)
// through the value-returning harnesses. A *firing* assert calls `exit(1)`,
// which the wasm harness (`wasmtime run --invoke main`, reads main's returned
// value) can't observe — the process just aborts with no return value — so the
// abort cases run on the three exit-code-observing backends. (wasm's abort
// path is identical IR — `if (!cond) { eprint; exit(1); }` — and is exercised
// by the passing wasm cases proving the desugar compiles + runs there.)
func TestAssert(t *testing.T) {
	// Passing asserts: main returns normally → observable on every backend.
	passCases := []struct {
		name string
		src  string
		want int
	}{
		// Both asserts pass → the function's own return value is the exit code.
		{"pass_both", `function main(): i32 {
    var n: i32 = 5;
    assert(n > 0, "n must be positive");
    assert(n < 100);
    return 7;
}`, 7},
		// The condition is an arbitrary runtime expression, not just a literal.
		{"runtime_cond_pass", `function sq(x: i32): i32 { return x * x; }
function main(): i32 {
    assert(sq(4) == 16, "square");
    return 9;
}`, 9},
		// The condition side effect runs exactly once (assert evaluates `cond`
		// a single time — it becomes the `if` test, not duplicated). A Cell
		// bumped by the condition call must read 1, not 2.
		{"cond_evaluated_once", `function bump(c: Cell[i32]): boolean { c.set(c.get() + 1); return true; }
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    assert(bump(a), "once");
    return a.get();
}`, 1},
		// `assert` stays usable as an ordinary identifier: only the
		// statement-position `assert (` shape is intercepted. A local named
		// `assert` and a reference to it in expression position are unaffected.
		{"identifier_still_usable", `function main(): i32 {
    var assert: i32 = 5;
    return assert + 2;
}`, 7},
	}
	for _, c := range passCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Run("interp", func(t *testing.T) {
				if code := runInterpByte(t, c.src); code != c.want {
					t.Errorf("interp exit = %d, want %d", code, c.want)
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
			t.Run("wasm32-wasi", func(t *testing.T) {
				if code := compileAndRunWasmbinMain(t, c.src); code != c.want {
					t.Errorf("wasm exit = %d, want %d", code, c.want)
				}
			})
		})
	}

	// Firing asserts: `exit(1)` aborts before any return, so these run on the
	// exit-code-observing backends only.
	abortCases := []struct {
		name string
		src  string
	}{
		// A false assert with a message aborts with exit 1.
		{"fail_with_msg", `function main(): i32 {
    var n: i32 = 0 - 3;
    assert(n > 0, "n must be positive");
    return 42;
}`},
		// A false assert with no message also aborts with exit 1.
		{"fail_no_msg", `function main(): i32 {
    assert(1 > 2);
    return 42;
}`},
		// The first assert passes; the second fails → still exit 1 (the abort
		// happens at the failing one, after the earlier ones ran).
		{"pass_then_fail", `function main(): i32 {
    assert(true);
    assert(false, "second");
    return 42;
}`},
	}
	for _, c := range abortCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Run("interp", func(t *testing.T) {
				if code := runInterpByte(t, c.src); code != 1 {
					t.Errorf("interp exit = %d, want 1 (assert should abort)", code)
				}
			})
			t.Run("arm64-linux", func(t *testing.T) {
				if _, code := compileAndRunArm64(t, c.src); code != 1 {
					t.Errorf("arm64 exit = %d, want 1 (assert should abort)", code)
				}
			})
			t.Run("x86_64", func(t *testing.T) {
				if _, code := compileAndRunX86_64(t, c.src); code != 1 {
					t.Errorf("x86_64 exit = %d, want 1 (assert should abort)", code)
				}
			})
		})
	}
}
