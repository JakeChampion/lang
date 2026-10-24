package e2e

import "testing"

func TestAssert(t *testing.T) {
	passCases := []struct {
		name string
		src  string
		want int
	}{
		{"pass_both", `function main(): i32 {
    let n: i32 = 5;
    assert(n > 0, "n must be positive");
    assert(n < 100);
    return 7;
}`, 7},
		{"runtime_cond_pass", `function sq(x: i32): i32 { return x * x; }
function main(): i32 {
    assert(sq(4) == 16, "square");
    return 9;
}`, 9},
		{"cond_evaluated_once", `function bump(c: Cell[i32]): boolean { c.set(c.get() + 1); return true; }
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    assert(bump(a), "once");
    return a.get();
}`, 1},
		{"identifier_still_usable", `function main(): i32 {
    let assert: i32 = 5;
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

	abortCases := []struct {
		name string
		src  string
	}{
		{"fail_with_msg", `function main(): i32 {
    let n: i32 = 0 - 3;
    assert(n > 0, "n must be positive");
    return 42;
}`},
		{"fail_no_msg", `function main(): i32 {
    assert(1 > 2);
    return 42;
}`},
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
