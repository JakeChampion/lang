package e2e

import "testing"

// A call `m.f(a, …)`, where module m has a method f and no other member f, is
// the method call `a.f(…)`. Thus `xs |> m.f()` reaches a method (#14398).
func TestPipeReachesMethod(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"pipe_to_method", `import "std/array";
function main(): i32 {
    let xs = [1, 2, 3, 4];
    return xs |> array.sum();
}`, 10},
		{"qualified_call_to_method", `import "std/array";
function main(): i32 {
    return array.sum([5, 6]);
}`, 11},
		{"pipe_chain_mixes_function_and_method", `import "std/array";
import "std/option";
function main(): i32 {
    return [1, 2, 3] |> array.map((x: i32) => x * 10) |> array.max() |> option.unwrap_or(0);
}`, 30},
		{"pipe_to_method_with_arguments", `import "std/array";
function main(): i32 {
    let ys = [1, 2, 3, 4] |> array.take_while((x: i32) => x < 3);
    return ys.len();
}`, 2},
		{"hole_picks_the_receiver_argument", `import "std/option";
function main(): i32 {
    let none: Option[i32] = None;
    return 7 |> option.unwrap_or(none, _);
}`, 7},
		{"local_shadows_the_module", `import "std/array";
function main(): i32 {
    let array = [4, 5, 6];
    return array.len();
}`, 3},
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
