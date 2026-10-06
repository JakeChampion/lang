package e2e

import (
	"os"
	"testing"
)

// TestRCNestedArrayAliasingBug gates a fixed reference-counting bug: an
// aliasing-built nested-array structure (a ChunkDef[] whose .body is a
// BodyLine[] of structs with string fields) is consumed by an
// expand-style function. `def_body.append(body[k])` copies the inner
// BodyLine structs — and their string pointers — out of `body` without
// an inc (the borrow model), so `body` had to be tainted in
// computeFreeEligible or its scope-exit deep-drop would free the
// strings the retained `def_body` still aliased. Before the fix the
// escape analysis only tainted a directly-named pushed element, missing
// the `body[k]` projection: native + wasm aborted (x86 255 / arm64 137
// / wasm trap) while the interpreter stayed correct.
//
// The reproducer (testdata/rc_nested_array_aliasing_bug.fern, with the
// full bisection write-up) tangles a one-line root chunk, so the
// correct exit code is len("fn main() {}") == 12. The fix taints the
// root container of a pointer-shaped projection at the non-inc retain
// sinks. This test asserts every backend now matches the interpreter.
func TestRCNestedArrayAliasingBug(t *testing.T) {
	src, err := os.ReadFile("testdata/rc_nested_array_aliasing_bug.fern")
	if err != nil {
		t.Fatalf("read reproducer: %v", err)
	}
	const want = 12 // len("fn main() {}")

	// One sub-test per backend: a toolchain gate inside any runner is a
	// t.Skip, which ends the whole test function and takes the legs after it
	// with it. Only the x86-64 leg had ever run, for a bug that reproduced
	// differently on each backend (x86 255 / arm64 137 / wasm trap).
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64(t, string(src)); code != want {
			t.Errorf("x86-64 exit = %d, want %d (interp result) — RC use-after-free regressed", code, want)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		if _, code := compileAndRunArm64(t, string(src)); code != want {
			t.Errorf("arm64 exit = %d, want %d (interp result) — RC use-after-free regressed", code, want)
		}
	})
	t.Run("wasm32-wasi", func(t *testing.T) {
		if code := compileAndRunWasmbinMain(t, string(src)); code != want {
			t.Errorf("wasm exit = %d, want %d (interp result) — RC use-after-free regressed", code, want)
		}
	})
}
