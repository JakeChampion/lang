package e2e

import "testing"

// TestDeferInBlockExpr pins `defer` in the two places the IR's defer collector
// could not see it (#6852): the block body of an arrow lambda, and any other
// value-position block.
//
// Both reduce to one shape. A `{ … }` in value position is an *ast.BlockExpr,
// an EXPRESSION that carries statements, and the collector walked statement
// structure only — so the defer never got its active-flag slot, the body walk
// then reached the same node and failed its pointer lookup, and every compiled
// backend refused the module with "ir: Defer node not registered (compiler
// bug)" while the interpreter ran it. An arrow lambda written with a block body
// is exactly that: the block is the lambda's value expression.
//
// The scope rule is the one #6851 settled, applied unchanged — a defer runs
// when the scope that reached it finishes, which is the enclosing loop
// iteration for a defer in a loop body and otherwise the enclosing FUNCTION. A
// block expression is not a function and not a loop, so a defer inside one
// fires at the exit of the function containing the block; inside a lambda, that
// function is the lambda.
//
// Every case encodes its contract in main's exit code (kept under 126, which
// wasmtime cannot express) so the same source runs on all four legs.
func TestDeferInBlockExpr(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		// The headline repro. The action has already run when f returns, so
		// the cell reads 1 at the call site: 5*10 + 1. Firing at main's exit
		// instead would leave 50.
		{"arrow_lambda_block_body", `function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var f = (n: i32): i32 => { defer a.set(a.get() + 1); return n; };
    var inside: i32 = f(5);
    return inside * 10 + a.get();
}`, 51},
		// The `function (…) { … }` spelling of the same lambda, whose body is
		// a plain Block rather than a block expression.
		{"function_form_lambda", `function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var f = function (n: i32): i32 { defer a.set(a.get() + 1); return n; };
    var inside: i32 = f(5);
    return inside * 10 + a.get();
}`, 51},
		// A defer under an `if` inside the lambda: still the lambda's exit,
		// not the end of the `if` body. 1 + 0 read inside, then 7.
		{"if_body_inside_lambda", `function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var f = (n: i32): i32 => {
        if (n > 0) { defer a.set(a.get() + 7); }
        return n + a.get();
    };
    var inside: i32 = f(1);
    return inside * 10 + a.get();
}`, 17},
		// A match ARM body in value position is a block expression too, so the
		// arm's defer is a defer inside a block inside a block.
		{"match_arm_inside_lambda", `function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var f = (n: i32): i32 => {
        var r: i32 = match (n) {
            1 => { defer a.set(a.get() + 4); 10 },
            _ => 20,
        };
        return r + a.get();
    };
    var inside: i32 = f(1);
    return inside * 10 + a.get();
}`, 104},
		// A defer in a LOOP inside a lambda composes the two rules: per
		// iteration (#6851), and inside the lambda. Three runs, all finished
		// before the lambda returns.
		{"while_loop_inside_lambda", `function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var f = (n: i32): i32 => {
        var i: i32 = 0;
        while (i < 3) {
            defer a.set(a.get() + 1);
            i = i + 1;
        }
        return a.get();
    };
    var inside: i32 = f(0);
    return inside * 10 + a.get();
}`, 33},
		// Each of those runs reads ITS iteration's local: 0 + 3 + 6. One run
		// at the lambda's exit would add 6 alone.
		{"loop_inside_lambda_reads_own_local", `function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var f = (n: i32): i32 => {
        for i in 0..3 {
            var k: i32 = i * 3;
            defer a.set(a.get() + k);
        }
        return a.get();
    };
    var inside: i32 = f(0);
    return inside * 10 + a.get();
}`, 99},
		// An errdefer in a lambda returning Result: fires on the Err exit.
		{"errdefer_in_lambda_err", `function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var f = (x: i32): Result[i32, i32] => {
        errdefer a.set(9);
        if (x < 0) { return Err(1); }
        return Ok(x);
    };
    match (f(-1)) { Ok(v) => {}, Err(e) => {} }
    return a.get();
}`, 9},
		// … and not on the Ok exit.
		{"errdefer_in_lambda_ok", `function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var f = (x: i32): Result[i32, i32] => {
        errdefer a.set(9);
        if (x < 0) { return Err(1); }
        return Ok(x);
    };
    match (f(5)) { Ok(v) => {}, Err(e) => {} }
    return a.get();
}`, 0},
		// Each lambda owns its defers: the inner one runs at the inner's exit
		// (a = 1), the outer's at the outer's (a = 12).
		{"nested_lambdas_own_their_defers", `function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var outer = (n: i32): i32 => {
        var inner = (m: i32): i32 => { defer a.set(a.get() * 10 + 1); return m; };
        var v: i32 = inner(n);
        defer a.set(a.get() * 10 + 2);
        return v;
    };
    var r: i32 = outer(7);
    return r * 10 + a.get();
}`, 82},
		// No lambda at all: a defer in a plain value-position block is scoped
		// to the enclosing FUNCTION. It has not run when g reads the cell (30)
		// and has when main does (+1).
		{"value_block_defer_is_function_scoped", `function g(a: Cell[i32]): i32 {
    var x: i32 = { defer a.set(a.get() + 1); 3 };
    return x * 10 + a.get();
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var inside: i32 = g(a);
    return inside + a.get();
}`, 31},
		// A loop nested inside a value block keeps the per-iteration rule: the
		// three runs are done by the time the block yields its value.
		{"loop_inside_value_block", `function g(a: Cell[i32]): i32 {
    var x: i32 = { var i: i32 = 0; while (i < 3) { defer a.set(a.get() + 1); i = i + 1; } 4 };
    return x * 10 + a.get();
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    return g(a);
}`, 43},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Run("interp", func(t *testing.T) {
				if code := runInterpByte(t, c.src); code != c.want {
					t.Errorf("interp exit = %d, want %d", code, c.want)
				}
			})
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
