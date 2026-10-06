package e2e

import (
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestASCIIByteTextInterp(t *testing.T) {
	if got := runInterpExit(t, e2eharness.ASCIIByteTextProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestASCIIByteTextX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, e2eharness.ASCIIByteTextProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestASCIIByteTextArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, e2eharness.ASCIIByteTextProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestASCIIByteTextWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, e2eharness.ASCIIByteTextProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestArm64DarwinASCIIByteText(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("arm64-darwin execution requires Apple Silicon")
	}
	if got := runArm64Darwin(t, e2eharness.ASCIIByteTextProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}
