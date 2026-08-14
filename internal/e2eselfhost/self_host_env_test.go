package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/testenv"
)

// TestSelfHostEnvX86_64 exercises the self-hosted x86-64 emitter's
// `env(name)` builtin (std/test plan: the OS-syscall batch). env walks
// the envp vector saved at _start and returns Option[string]:
// Some(value) when the variable is set, None otherwise. The compiled
// program is run twice — once with the variable set (expect exit 7),
// once without it (expect the None arm, exit 1).
func TestSelfHostEnvX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	prog := `function main(): i32 {
    match (env("FERN_SELFHOST_ENV_TEST")) {
        Some(v) => {
            if (v == "hello-env") { return 7; }
            return 2;
        },
        None => { return 1; }
    }
}`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "envprog", string(asm))

	mkCmd := func() *exec.Cmd {
		if len(runner) == 0 {
			return exec.Command(progBin)
		}
		return exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
	}

	t.Run("set", func(t *testing.T) {
		cmd := mkCmd()
		cmd.Env = testenv.With("FERN_SELFHOST_ENV_TEST=hello-env")
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 7 {
			t.Errorf("env(set) exited %d, want 7 (1=None, 2=value mismatch)", code)
		}
	})
	t.Run("unset", func(t *testing.T) {
		cmd := mkCmd()
		cmd.Env = testenv.Clean()
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 1 {
			t.Errorf("env(unset) exited %d, want 1 (None arm)", code)
		}
	})
}

// TestSelfHostEnvArm64 is the ARM64 counterpart: the self-hosted ARM64
// emitter's env(name). The asm_ir_run (-target arm64-linux) driver (an x86 host binary)
// compiles the same lookup program to aarch64 asm; the assembled binary
// runs under qemu-aarch64 (which forwards the environment to the guest),
// expecting exit 7 when the variable is set and 1 (None) when it isn't.
func TestSelfHostEnvArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverBin := buildBin(t, x86gcc, dir, "driver", asm)

	envSrc := `function main(): i32 {
    match (env("FERN_SELFHOST_ENV_TEST")) {
        Some(v) => {
            if (v == "hello-env") { return 7; }
            return 2;
        },
        None => { return 1; }
    }
}`
	envAsm := runCapture(t, x86gcc, x86runner, driverBin, []byte(envSrc), "-target", "arm64-linux")
	if len(envAsm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the env program")
	}
	envBin := buildBin(t, arm64gcc, dir, "envprog", string(envAsm))

	t.Run("set", func(t *testing.T) {
		cmd := runArm64Bin(qemu, envBin)
		cmd.Env = testenv.With("FERN_SELFHOST_ENV_TEST=hello-env")
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 7 {
			t.Errorf("env(set) exited %d, want 7 (1=None, 2=value mismatch)", code)
		}
	})
	t.Run("unset", func(t *testing.T) {
		cmd := runArm64Bin(qemu, envBin)
		cmd.Env = testenv.Clean()
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 1 {
			t.Errorf("env(unset) exited %d, want 1 (None arm)", code)
		}
	})
}
