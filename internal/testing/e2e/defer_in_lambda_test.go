package e2e

import "testing"

func TestDeferInBlockExpr(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"arrow_lambda_block_body", `function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let f = (n: i32): i32 => { defer a.set(a.get() + 1); return n; };
    let inside: i32 = f(5);
    return inside * 10 + a.get();
}`, 51},
		{"function_form_lambda", `function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let f = (n: i32): i32 => { defer a.set(a.get() + 1); return n; };
    let inside: i32 = f(5);
    return inside * 10 + a.get();
}`, 51},
		{"if_body_inside_lambda", `function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let f = (n: i32): i32 => {
        if (n > 0) { defer a.set(a.get() + 7); }
        return n + a.get();
    };
    let inside: i32 = f(1);
    return inside * 10 + a.get();
}`, 17},
		{"match_arm_inside_lambda", `function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let f = (n: i32): i32 => {
        let r: i32 = match (n) {
            1 => { defer a.set(a.get() + 4); 10 },
            _ => 20,
        };
        return r + a.get();
    };
    let inside: i32 = f(1);
    return inside * 10 + a.get();
}`, 104},
		{"while_loop_inside_lambda", `function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let f = (n: i32): i32 => {
        let i: i32 = 0;
        while (i < 3) {
            defer a.set(a.get() + 1);
            i = i + 1;
        }
        return a.get();
    };
    let inside: i32 = f(0);
    return inside * 10 + a.get();
}`, 33},
		{"loop_inside_lambda_reads_own_local", `function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let f = (n: i32): i32 => {
        for i in 0..3 {
            let k: i32 = i * 3;
            defer a.set(a.get() + k);
        }
        return a.get();
    };
    let inside: i32 = f(0);
    return inside * 10 + a.get();
}`, 99},
		{"errdefer_in_lambda_err", `function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let f = (x: i32): Result[i32, i32] => {
        errdefer a.set(9);
        if (x < 0) { return Err(1); }
        return Ok(x);
    };
    match (f(-1)) { Ok(v) => {}, Err(e) => {} }
    return a.get();
}`, 9},
		{"errdefer_in_lambda_ok", `function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let f = (x: i32): Result[i32, i32] => {
        errdefer a.set(9);
        if (x < 0) { return Err(1); }
        return Ok(x);
    };
    match (f(5)) { Ok(v) => {}, Err(e) => {} }
    return a.get();
}`, 0},
		{"nested_lambdas_own_their_defers", `function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let outer = (n: i32): i32 => {
        let inner = (m: i32): i32 => { defer a.set(a.get() * 10 + 1); return m; };
        let v: i32 = inner(n);
        defer a.set(a.get() * 10 + 2);
        return v;
    };
    let r: i32 = outer(7);
    return r * 10 + a.get();
}`, 82},
		{"value_block_defer_is_function_scoped", `function g(a: Cell[i32]): i32 {
    let x: i32 = { defer a.set(a.get() + 1); 3 };
    return x * 10 + a.get();
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let inside: i32 = g(a);
    return inside + a.get();
}`, 31},
		{"loop_inside_value_block", `function g(a: Cell[i32]): i32 {
    let x: i32 = { let i: i32 = 0; while (i < 3) { defer a.set(a.get() + 1); i = i + 1; } 4 };
    return x * 10 + a.get();
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
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
