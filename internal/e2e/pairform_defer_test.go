package e2e

import "testing"

// TestPairFormFuncWithDeferReturnsCorrectly is a regression test for a
// miscompile of enum-returning functions that are eligible for the
// pair-form (tag, payload) return ABI but also register a `defer`.
//
// findPairFormFuncs marked such a function pair-form (giving it a two-i32
// return signature), but the Return lowering's pair-form fast path is
// gated on `len(b.defers) == 0` and fell back to the heap-box path when
// defers were present — emitting a single-pointer OpReturn against the
// two-i32 signature. The result was a real miscompile on every backend:
// the wasm validator rejected it ("expected i32 but nothing on stack"),
// and the natives returned a heap-box pointer as the tag plus a garbage
// payload register, so the caller's match read the wrong variant.
//
// Each case observes the FORWARDED payload (the earlier symptom only
// surfaced when the caller discarded the return value, hiding the native
// half), and the defer additionally writes a sentinel so we confirm it
// still runs. Pre-fix: arm64 / x86-64 returned 0 and wasm failed to
// validate. Post-fix: every backend returns the payload.
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
    var a: Cell[i32] = cell_new(0);
    var r: i32 = 0;
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
    var a: Cell[i32] = cell_new(0);
    var r: i32 = 0;
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
