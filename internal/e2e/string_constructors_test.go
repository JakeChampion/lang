package e2e

import (
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestStringConstructorsInterp(t *testing.T) {
	if got := runInterpExit(t, e2eharness.StringConstructorsProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestStringConstructorsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, e2eharness.StringConstructorsProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestStringConstructorsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, e2eharness.StringConstructorsProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestStringConstructorsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, e2eharness.StringConstructorsProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestArm64DarwinStringConstructors(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("arm64-darwin execution requires Apple Silicon")
	}
	if got := runArm64Darwin(t, e2eharness.StringConstructorsProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}
