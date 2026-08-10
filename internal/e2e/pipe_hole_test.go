package e2e

import "testing"

// TestPipePlaceholder exercises the `_` topic placeholder in piped calls:
// `x |> f(a, _)` substitutes the LHS at the hole instead of prepending it
// (`f(a, x)`), for callees that don't take the piped value first. Pure
// parse-time desugar — every backend sees an ordinary Call — so all cases
// are value-returning and run on all four backends. Composition matters:
// a nested pipe consumes its own `_` before the outer scan runs.
func TestPipePlaceholder(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		// sub(10, 3) = 7: hole in the second slot.
		{"hole_second_arg", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 {
    var x: i32 = 3;
    return x |> sub(10, _);
}`, 7},
		// sub(3, 1) = 2: hole in the first slot.
		{"hole_first_arg", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 {
    var x: i32 = 3;
    return x |> sub(_, 1);
}`, 2},
		// Position-distinguishing three-arg case: pick returns
		// a + b*10 + c, so which slot the LHS lands in changes the
		// result. Hole at b: pick(1, 20, 3) = 1 + 200 + 3 = 204
		// (prepending would have computed pick(20, 1, 3) = 33).
		{"hole_middle_of_three", `function pick(a: i32, b: i32, c: i32): i32 { return a + b * 10 + c; }
function main(): i32 {
    return 20 |> pick(1, _, 3);
}`, 204},
		// Nested pipes: inner hole binds to the inner LHS, outer to the outer.
		// sub(20, sub(5, 3)) = sub(20, 2) = 18.
		{"nested_holes_compose", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 {
    var x: i32 = 3;
    return 20 |> sub(_, x |> sub(5, _));
}`, 18},
		// Chained pipes, each with its own hole. sub(9, 4)=5 then sub(8, 5)=3.
		{"chained_holes", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 {
    return 4 |> sub(9, _) |> sub(8, _);
}`, 3},
		// Mixing: a plain (prepending) pipe stage after a hole stage.
		// sub(10, 4)=6, then 6 |> sub(2) = sub(6, 2) = 4.
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
