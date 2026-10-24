package e2e

import (
	"strings"
	"testing"
)

func backendsAgree(t *testing.T, src, want string) {
	t.Helper()
	t.Run("wasm32-wasi", func(t *testing.T) {
		if got := runWasmCapturingStdout(t, src); got != want {
			t.Errorf("wasm = %q, want %q", got, want)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		got, code := compileAndRunX86_64(t, src)
		got = strings.TrimSpace(got)
		if code != 0 {
			t.Fatalf("x86-64 exit = %d, want 0; stdout:\n%s", code, got)
		}
		if got != want {
			t.Errorf("x86-64 = %q, want %q", got, want)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		got, code := compileAndRunArm64(t, src)
		got = strings.TrimSpace(got)
		if code != 0 {
			t.Fatalf("arm64 exit = %d, want 0; stdout:\n%s", code, got)
		}
		if got != want {
			t.Errorf("arm64 = %q, want %q", got, want)
		}
	})
}

func interpOracle(t *testing.T, src, want string) string {
	t.Helper()
	got := dynInterpStdout(t, src)
	if got != want {
		t.Fatalf("interp baseline = %q, want %q", got, want)
	}
	return got
}
