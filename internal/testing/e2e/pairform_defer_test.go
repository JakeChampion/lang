package e2e

import "testing"

func TestPairFormFuncWithDeferReturnsCorrectly(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"option_some_payload_with_defer", `function f(out: Cell[i32]): Option[i32] {
    defer out.set(1);
    return Some(42);
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let r: i32 = 0;
    match (f(a)) {
        Some(v) => { r = v; },
        None => { r = 100; },
    }
    return r + a.get();
}`, 43},
		{"result_ok_payload_with_defer", `function g(out: Cell[i32]): Result[i32, i32] {
    defer out.set(1);
    return Ok(7);
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    let r: i32 = 0;
    match (g(a)) {
        Ok(v) => { r = v; },
        Err(e) => { r = 100; },
    }
    return r + a.get();
}`, 8},
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
