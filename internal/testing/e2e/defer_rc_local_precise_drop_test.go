package e2e

import "testing"

func TestDeferReadLocalKeepsItLive(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"defer_is_only_use", `function f(out: Cell[i32]): i32 {
    let arr: i32[] = [1, 2, 3];
    defer out.set(arr[1]);
    return 0;
}
function main(): i32 { let a: Cell[i32] = cell_new(0); f(a); return a.get(); }`, 2},
		{"last_use_before_defer", `function f(out: Cell[i32]): i32 {
    let arr: i32[] = [1, 2, 3];
    out.set(arr[0]);
    defer out.set(arr[1]);
    return 0;
}
function main(): i32 { let a: Cell[i32] = cell_new(0); f(a); return a.get(); }`, 2},
		{"silent_wrong_answer", `function f(out: Cell[i32]): i32 {
    let arr: i32[] = [1, 2, 3];
    defer out.set(arr.len());
    return 0;
}
function main(): i32 { let a: Cell[i32] = cell_new(0); f(a); return a.get(); }`, 3},
		{"later_use_is_the_return", `function f(out: Cell[i32]): i32 {
    let arr: i32[] = [1, 2, 3];
    defer out.set(arr[1]);
    return arr[0];
}
function main(): i32 { let a: Cell[i32] = cell_new(0); f(a); return a.get(); }`, 2},
		{"later_use_is_not_a_return", `function sink(c: Cell[i32], v: i32): i32 { c.set(c.get() + v); return v; }
function f(out: Cell[i32]): i32 {
    let arr: i32[] = [1, 2, 3];
    defer out.set(out.get() + arr[1]);
    sink(out, arr[0]);
    return 0;
}
function main(): i32 { let a: Cell[i32] = cell_new(0); f(a); return a.get(); }`, 3},
		{"errdefer_reads_local", `function f(out: Cell[i32]): Option[i32] {
    let arr: i32[] = [1, 2, 3];
    errdefer out.set(arr[1]);
    return None;
}
function main(): i32 {
    let a: Cell[i32] = cell_new(0);
    match (f(a)) { Some(v) => { return 90; }, None => { return a.get(); } }
}`, 2},
		{"defer_in_loop_over_loop_local", `function f(out: Cell[i32]): i32 {
    let i: i32 = 0;
    while (i < 4) {
        let arr: i32[] = [i, i + 1, i + 2];
        defer out.set(out.get() + arr[1]);
        if (i == 2) {
            let brr: i32[] = [9, 9];
            out.set(out.get() + brr[0]);
            i = i + 1;
            continue;
        }
        i = i + 1;
    }
    return 0;
}
function main(): i32 { let a: Cell[i32] = cell_new(0); f(a); return a.get(); }`, 19},
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
					t.Errorf("arm64 exit = %d, want %d (139 = the zeroed slot read as a pointer)", code, c.want)
				}
			})
			t.Run("x86_64", func(t *testing.T) {
				if _, code := compileAndRunX86_64(t, c.src); code != c.want {
					t.Errorf("x86_64 exit = %d, want %d (139 = the zeroed slot read as a pointer)", code, c.want)
				}
			})
		})
	}
}
