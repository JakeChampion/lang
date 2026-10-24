package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostEnvX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

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
		cmd.Env = append(os.Environ(), "FERN_SELFHOST_ENV_TEST=hello-env")
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 7 {
			t.Errorf("env(set) exited %d, want 7 (1=None, 2=value mismatch)", code)
		}
	})
	t.Run("unset", func(t *testing.T) {
		cmd := mkCmd()
		var env []string
		for _, kv := range os.Environ() {
			if len(kv) >= 22 && kv[:22] == "FERN_SELFHOST_ENV_TEST" {
				continue
			}
			env = append(env, kv)
		}
		cmd.Env = env
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 1 {
			t.Errorf("env(unset) exited %d, want 1 (None arm)", code)
		}
	})
	nearMisses := []string{"F=1", "FERN_SELFHOST_ENV_TES=short", "FERN_SELFHOST_ENV_TESTX=long", "=", "FERN"}
	t.Run("near-misses-first", func(t *testing.T) {
		cmd := mkCmd()
		cmd.Env = append(append([]string{}, nearMisses...), "FERN_SELFHOST_ENV_TEST=hello-env")
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 7 {
			t.Errorf("env(after near misses) exited %d, want 7 (1=None, 2=value mismatch)", code)
		}
	})
	t.Run("near-misses-only", func(t *testing.T) {
		cmd := mkCmd()
		cmd.Env = nearMisses
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 1 {
			t.Errorf("env(near misses only) exited %d, want 1 (None arm)", code)
		}
	})
}

func TestSelfHostEnvArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
		cmd.Env = append(os.Environ(), "FERN_SELFHOST_ENV_TEST=hello-env")
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 7 {
			t.Errorf("env(set) exited %d, want 7 (1=None, 2=value mismatch)", code)
		}
	})
	t.Run("unset", func(t *testing.T) {
		cmd := runArm64Bin(qemu, envBin)
		var env []string
		for _, kv := range os.Environ() {
			if len(kv) >= 22 && kv[:22] == "FERN_SELFHOST_ENV_TEST" {
				continue
			}
			env = append(env, kv)
		}
		cmd.Env = env
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 1 {
			t.Errorf("env(unset) exited %d, want 1 (None arm)", code)
		}
	})
}
