package e2e

import "testing"

// TestTodo exercises the `todo;` / `todo("msg");` stub statement. It is a
// parser-level desugar to `loop { eprint("todo[: msg]"); exit(101); }` —
// the `loop` wrapper makes the stub DIVERGE for the checker's
// missing-return (E052) and `let else` analyses, so a bare `todo;` can
// stand in for a whole non-void function body while the code is being
// written. The whole contract is observable through the process exit code:
// a reached todo aborts with exit 101, an unreached one leaves the
// program's own control flow intact.
//
// Same case split as TestAssert (the desugar template): unreached-todo
// programs return normally → all four backends; reached-todo programs call
// `exit(101)`, which the wasm `--invoke main` harness can't observe → the
// three exit-code-observing backends.
func TestTodo(t *testing.T) {
	// Unreached todos: main returns normally → observable on every backend.
	passCases := []struct {
		name string
		src  string
		want int
	}{
		// The stubbed branch isn't taken; the live branch returns. This is
		// the E052 shape too: `helper` is a non-void function whose body is
		// nothing but `todo;`, and it must pass the missing-return check.
		{"stub_branch_not_taken", `function helper(): i32 {
    todo;
}
function main(): i32 {
    var n: i32 = 2;
    if (n > 5) {
        return helper();
    }
    return 9;
}`, 9},
		// A stub with a message, also not reached.
		{"stub_msg_not_taken", `function helper(): i32 {
    todo("write the real helper");
}
function main(): i32 {
    if (false) {
        return helper();
    }
    return 4;
}`, 4},
		// `todo` in a `let else` divergent-else block: the else must
		// diverge (the checker enforces it), and the todo's loop-shaped
		// desugar satisfies that. The Some path runs normally.
		{"let_else_diverges", `function main(): i32 {
    var o: Option[i32] = Some(6);
    let Some(x) = o else { todo("handle None"); };
    return x + 1;
}`, 7},
		// `todo` stays usable as an ordinary identifier: only the
		// statement-position `todo ;` / `todo (` shapes are intercepted.
		{"identifier_still_usable", `function main(): i32 {
    var todo: i32 = 5;
    todo = todo + 1;
    return todo + 2;
}`, 8},
	}
	for _, c := range passCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Run("interp", func(t *testing.T) {
				if code := runInterpByte(t, c.src); code != c.want {
					t.Errorf("interp exit = %d, want %d", code, c.want)
				}
			})
			t.Run("arm64", func(t *testing.T) {
				if _, code := compileAndRunArm64(t, c.src); code != c.want {
					t.Errorf("arm64 exit = %d, want %d", code, c.want)
				}
			})
			t.Run("x86_64", func(t *testing.T) {
				if _, code := compileAndRunX86_64(t, c.src); code != c.want {
					t.Errorf("x86_64 exit = %d, want %d", code, c.want)
				}
			})
			t.Run("wasm", func(t *testing.T) {
				if code := compileAndRunWasmbinMain(t, c.src); code != c.want {
					t.Errorf("wasm exit = %d, want %d", code, c.want)
				}
			})
		})
	}

	// Reached todos: `exit(101)` aborts before any return → the three
	// exit-code-observing backends.
	abortCases := []struct {
		name string
		src  string
	}{
		// A whole-function stub, reached → 101.
		{"function_body_stub", `function helper(): i32 {
    todo("not written yet");
}
function main(): i32 {
    return helper();
}`},
		// Bare form, reached mid-function.
		{"bare_reached", `function main(): i32 {
    var n: i32 = 1;
    if (n == 1) {
        todo;
    }
    return 42;
}`},
		// The `let else` None path lands in the todo.
		{"let_else_none_path", `function main(): i32 {
    var o: Option[i32] = None;
    let Some(x) = o else { todo("handle None"); };
    return x;
}`},
	}
	for _, c := range abortCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Run("interp", func(t *testing.T) {
				if code := runInterpByte(t, c.src); code != 101 {
					t.Errorf("interp exit = %d, want 101 (todo should abort)", code)
				}
			})
			t.Run("arm64", func(t *testing.T) {
				if _, code := compileAndRunArm64(t, c.src); code != 101 {
					t.Errorf("arm64 exit = %d, want 101 (todo should abort)", code)
				}
			})
			t.Run("x86_64", func(t *testing.T) {
				if _, code := compileAndRunX86_64(t, c.src); code != 101 {
					t.Errorf("x86_64 exit = %d, want 101 (todo should abort)", code)
				}
			})
		})
	}
}
