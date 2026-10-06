package e2e

import (
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const stringCharacterSetProgram = e2eharness.StringCharacterSetProgram

func TestStringCharacterSetInterp(t *testing.T) {
	if got := runInterpExit(t, stringCharacterSetProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestStringCharacterSetX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringCharacterSetProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestStringCharacterSetArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringCharacterSetProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestStringCharacterSetWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringCharacterSetProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestArm64DarwinStringCharacterSet(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("arm64-darwin execution requires Apple Silicon")
	}
	if got := runArm64Darwin(t, stringCharacterSetProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}
