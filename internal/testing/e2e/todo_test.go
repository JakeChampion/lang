package e2e

import "testing"

func TestTodo(t *testing.T) {
	passCases := []struct {
		name string
		src  string
		want int
	}{
		{"stub_branch_not_taken", `function helper(): i32 {
    todo;
}
function main(): i32 {
    let n: i32 = 2;
    if (n > 5) {
        return helper();
    }
    return 9;
}`, 9},
		{"stub_msg_not_taken", `function helper(): i32 {
    todo("write the real helper");
}
function main(): i32 {
    if (false) {
        return helper();
    }
    return 4;
}`, 4},
		{"let_else_diverges", `function main(): i32 {
    let o: Option[i32] = Some(6);
    let Some(x) = o else { todo("handle None"); };
    return x + 1;
}`, 7},
		{"identifier_still_usable", `function main(): i32 {
    let todo: i32 = 5;
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
		{"function_body_stub", `function helper(): i32 {
    todo("not written yet");
}
function main(): i32 {
    return helper();
}`},
		{"bare_reached", `function main(): i32 {
    let n: i32 = 1;
    if (n == 1) {
        todo;
    }
    return 42;
}`},
		{"let_else_none_path", `function main(): i32 {
    let o: Option[i32] = None;
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
			t.Run("arm64-linux", func(t *testing.T) {
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
