package e2e

import (
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestStreamUTF8Interp(t *testing.T) {
	if got := runInterpExit(t, e2eharness.StreamUTF8Program); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestStreamUTF8X86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, e2eharness.StreamUTF8Program); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestStreamUTF8Arm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, e2eharness.StreamUTF8Program); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestStreamUTF8Wasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, e2eharness.StreamUTF8Program); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestArm64DarwinStreamUTF8(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("arm64-darwin execution requires Apple Silicon")
	}
	if got := runArm64Darwin(t, e2eharness.StreamUTF8Program); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}
