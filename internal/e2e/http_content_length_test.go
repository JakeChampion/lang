package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestArm64DarwinHTTPContentLength(t *testing.T) {
	// buildAndRunDarwin requires exit 0; only adapt the shared probe's pass sentinel.
	src := strings.Replace(e2eharness.HTTPContentLengthProbe(), "return 42;", "return 0;", 1)
	buildAndRunDarwin(t, t.TempDir(), src)
}

func TestHttpContentLengthInterp(t *testing.T) {
	if got := runInterpExit(t, e2eharness.HTTPContentLengthProbe()); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestHttpContentLengthX86_64(t *testing.T) {
	if out, got := compileAndRunX86_64(t, e2eharness.HTTPContentLengthProbe()); got != 42 {
		t.Fatalf("x86-64 got %d, want 42; first failing case: %s", got, out)
	}
}

func TestHttpContentLengthArm64(t *testing.T) {
	if out, got := compileAndRunArm64(t, e2eharness.HTTPContentLengthProbe()); got != 42 {
		t.Fatalf("arm64 got %d, want 42; first failing case: %s", got, out)
	}
}

func TestHttpContentLengthWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, e2eharness.HTTPContentLengthProbe()); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}
