package e2e

import "testing"

// TestTryOpRunsDefersOnErrorPath is a regression test for a bug where the
// `expr?` (TryOp) failure path returned directly without running the
// function's registered `defer`s. Every other return site replays defers
// in LIFO order via emitDeferCleanup before leaving the function; the
// error-propagation path skipped it, so a `defer f.close()` (or any other
// cleanup) silently never ran when a `?` short-circuited on Err / None.
//
// Each case observes the side effect of a defer that runs only if the
// error path honoured it: the defer writes a sentinel into a caller-owned
// array, and main returns that slot. Pre-fix the slot keeps its initial
// value (defer skipped); post-fix it holds the sentinel.
//
// The fallible functions use a string error/payload so they lower to the
// heap-box (single-pointer) enum ABI rather than the (tag, payload)
// pair-form ABI — pair-form functions that also carry a defer hit a
// separate, pre-existing wasmbin codegen gap, so heap-form keeps the wasm
// leg exercising this fix instead of skipping. Both Result (`Err` →
// forwards the source box) and Option (`None` → fresh None) failure shapes
// are covered, on every backend, since the cleanup emission lives in the
// shared IR lowering.
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
    var x: i32 = fails()?;
    return Ok(x);
}
function main(): i32 {
    var c: Cell[i32] = cell_new(0);
    inner(c);
    return c.get();
}`, 42},
		{"option_none_path_runs_defer", `function maybe(): Option[string] {
    return None;
}
function inner(c: Cell[i32]): Option[string] {
    defer c.set(9);
    var x: string = maybe()?;
    return Some(x);
}
function main(): i32 {
    var c: Cell[i32] = cell_new(0);
    inner(c);
    return c.get();
}`, 9},
		{"multiple_defers_lifo_on_error_path", `function fails(): Result[i32, string] {
    return Err("e");
}
function inner(c: Cell[i32]): Result[i32, string] {
    defer c.set(10);
    defer c.set(20);
    var x: i32 = fails()?;
    return Ok(x);
}
function main(): i32 {
    var c: Cell[i32] = cell_new(0);
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
