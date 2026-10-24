package e2e

import (
	"os"
	"testing"
)

func TestRCNestedArrayAliasingBug(t *testing.T) {
	src, err := os.ReadFile("testdata/rc_nested_array_aliasing_bug.fern")
	if err != nil {
		t.Fatalf("read reproducer: %v", err)
	}
	const want = 12

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
