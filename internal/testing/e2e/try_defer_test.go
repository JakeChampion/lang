package e2e

import "testing"

func TestTryOpRunsDefersOnErrorPath(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"result_err_path_runs_defer", `function fails(): Result[i32, string] {
    return Err("boom");
}
function inner(c: Cell[i32]): Result[i32, string] {
    defer c.set(42);
    let x: i32 = fails()?;
    return Ok(x);
}
function main(): i32 {
    let c: Cell[i32] = cell_new(0);
    inner(c);
    return c.get();
}`, 42},
		{"option_none_path_runs_defer", `function maybe(): Option[string] {
    return None;
}
function inner(c: Cell[i32]): Option[string] {
    defer c.set(9);
    let x: string = maybe()?;
    return Some(x);
}
function main(): i32 {
    let c: Cell[i32] = cell_new(0);
    inner(c);
    return c.get();
}`, 9},
		{"multiple_defers_lifo_on_error_path", `function fails(): Result[i32, string] {
    return Err("e");
}
function inner(c: Cell[i32]): Result[i32, string] {
    defer c.set(10);
    defer c.set(20);
    let x: i32 = fails()?;
    return Ok(x);
}
function main(): i32 {
    let c: Cell[i32] = cell_new(0);
    inner(c);
    return c.get();
}`, 10},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
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
}
