package e2e

import (
	"os"
	"os/exec"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSeededRandomBytesInterp(t *testing.T) {
	if got := runInterpExit(t, e2eharness.SeededRandomBytesProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestSeededRandomBytesX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, e2eharness.SeededRandomBytesProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestSeededRandomBytesArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, e2eharness.SeededRandomBytesProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestSeededRandomBytesWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, e2eharness.SeededRandomBytesProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestArm64DarwinSeededRandomBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("arm64-darwin execution requires Apple Silicon")
	}
	if got := runArm64Darwin(t, e2eharness.SeededRandomBytesProgram); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
}

func TestArm64SSASeededRandomBytes(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	bin := compileArm64SSA(t, fern, e2eharness.SeededRandomBytesProgram, os.Environ())
	if out, err := runArm64Bin(qemu, bin).CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
}

func TestX86_64SSASeededRandomBytes(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	fern := buildFernCLI(t)
	bin := compileX86_64SSA(t, fern, e2eharness.SeededRandomBytesProgram, os.Environ())
	cmd := exec.Command(bin)
	if qemu != "" {
		cmd = exec.Command(qemu, bin)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
}
