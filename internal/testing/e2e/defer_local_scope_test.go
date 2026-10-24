package e2e

import "testing"

func TestDeferOverLocalRunsInInterp(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"function_level_local", `function f(out: Cell[i32]): i32 {
    let k: i32 = 7;
    defer out.set(out.get() + k);
    return 1;
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let r: i32 = f(a);
    return a.get() * 10 + r;
}`, 71},
		{"block_scoped_local", `function f(out: Cell[i32]): i32 {
    let n: i32 = 1;
    if (n > 0) {
        let k: i32 = 7;
        defer out.set(out.get() + k);
        n = n + 1;
    }
    return n;
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let r: i32 = f(a);
    return a.get() * 10 + r;
}`, 72},
		{"reads_value_at_exit", `function f(out: Cell[i32]): i32 {
    let k: i32 = 2;
    defer out.set(out.get() + k);
    k = 9;
    return 1;
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let r: i32 = f(a);
    return a.get() * 10 + r;
}`, 91},
		{"runs_at_function_exit", `function f(out: Cell[i32]): i32 {
    let k: i32 = 5;
    defer out.set(out.get() + k);
    return out.get();
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let before: i32 = f(a);
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
