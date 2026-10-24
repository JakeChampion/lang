package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func buildFernCLI(t *testing.T) string {
	t.Helper()
	return e2eharness.FernCLI(t)
}

func arm64QemuOrEmpty(t *testing.T) string {
	t.Helper()
	runner, ok := arm64Runner()
	if !ok {
		t.Skip("no qemu-aarch64 to run arm64 binaries")
	}
	return runner
}

func arm64Runner() (string, bool) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		return "", true
	}
	for _, c := range []string{"qemu-aarch64", "qemu-aarch64-static"} {
		if p, err := exec.LookPath(c); err == nil {
			return p, true
		}
	}
	return "", false
}

func TestArm64NativeIsCLIDefault(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 42; }\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	t.Run("default_build_is_native", func(t *testing.T) {
		qemu := arm64QemuOrEmpty(t)
		out := filepath.Join(dir, "prog.bin")
		if o, err := exec.Command(bin, "-target", "arm64-linux", "-o", out, src).CombinedOutput(); err != nil {
			t.Fatalf("default arm64 build failed: %v\n%s", err, o)
		}
		info, err := os.Stat(out)
		if err != nil {
			t.Fatalf("stat out: %v", err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("output binary not executable: %v", info.Mode())
		}
		cmd := runArm64Bin(qemu, out)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("native-built binary exit = %d, want 42", code)
		}
	})

	t.Run("run_flag_native", func(t *testing.T) {
		arm64QemuOrEmpty(t)
		cmd := exec.Command(bin, "-target", "arm64-linux", "-run", src)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("fern -run exit = %d, want 42", code)
		}
	})

}
