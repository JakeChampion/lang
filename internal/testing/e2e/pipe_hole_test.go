package e2e

import "testing"

func TestPipePlaceholder(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"hole_second_arg", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 {
    let x: i32 = 3;
    return x |> sub(10, _);
}`, 7},
		{"hole_first_arg", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 {
    let x: i32 = 3;
    return x |> sub(_, 1);
}`, 2},
		{"hole_middle_of_three", `function pick(a: i32, b: i32, c: i32): i32 { return a + b * 10 + c; }
function main(): i32 {
    return 20 |> pick(1, _, 3);
}`, 204},
		{"nested_holes_compose", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 {
    let x: i32 = 3;
    return 20 |> sub(_, x |> sub(5, _));
}`, 18},
		{"chained_holes", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 {
    return 4 |> sub(9, _) |> sub(8, _);
}`, 3},
		{"hole_then_prepend", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 {
    return 4 |> sub(10, _) |> sub(2);
}`, 4},
	}
	for _, c := range cases {
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
}
