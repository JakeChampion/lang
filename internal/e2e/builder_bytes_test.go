package e2e

import (
	"os"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
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

func TestArm64SSABuilderBytes(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	bin := compileArm64SSA(t, fern, e2eharness.BuilderBytesProgram, os.Environ())
	if code, stderr := runArm64SSABin(t, qemu, bin, t.TempDir(), os.Environ()); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, stderr)
	}
}

func TestX86_64SSABuilderBytes(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	fern := buildFernCLI(t)
	bin := compileX86_64SSA(t, fern, e2eharness.BuilderBytesProgram, os.Environ())
	if code, stderr := runX86_64SSABin(t, qemu, bin, t.TempDir(), os.Environ()); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, stderr)
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
