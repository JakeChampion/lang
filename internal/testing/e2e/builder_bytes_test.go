package e2e

import (
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestBuilderBytesInterp(t *testing.T) {
	if got := runInterpExit(t, e2eharness.BuilderBytesProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestBuilderBytesX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, e2eharness.BuilderBytesProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestBuilderBytesArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, e2eharness.BuilderBytesProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestBuilderBytesWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, e2eharness.BuilderBytesProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestArm64DarwinBuilderBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("arm64-darwin execution requires Apple Silicon")
	}
	if got := runArm64Darwin(t, e2eharness.BuilderBytesProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}
