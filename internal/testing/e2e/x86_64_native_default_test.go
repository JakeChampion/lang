package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func x86QemuOrEmpty(t *testing.T) string {
	t.Helper()
	runner, ok := x86Runner()
	if !ok {
		t.Skip("no qemu-x86_64 to run x86-64 binaries")
	}
	return runner
}

func x86Runner() (string, bool) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return "", true
	}
	for _, c := range []string{"qemu-x86_64", "qemu-x86_64-static"} {
		if p, err := exec.LookPath(c); err == nil {
			return p, true
		}
	}
	return "", false
}

func runX86Bin(qemu, binPath string, args ...string) *exec.Cmd {
	if qemu == "" {
		return exec.Command(binPath, args...)
	}
	return exec.Command(qemu, append([]string{binPath}, args...)...)
}

func TestX86_64NativeIsCLIDefault(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 42; }\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	t.Run("default_build_is_native", func(t *testing.T) {
		qemu := x86QemuOrEmpty(t)
		out := filepath.Join(dir, "prog.bin")
		if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, src).CombinedOutput(); err != nil {
			t.Fatalf("default x86-64 build failed: %v\n%s", err, o)
		}
		info, err := os.Stat(out)
		if err != nil {
			t.Fatalf("stat out: %v", err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("output binary not executable: %v", info.Mode())
		}
		cmd := runX86Bin(qemu, out)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("native-built binary exit = %d, want 42", code)
		}
	})

	t.Run("run_flag_native", func(t *testing.T) {
		if runtime.GOARCH != "amd64" {
			if _, err := exec.LookPath("qemu-x86_64"); err != nil {
				t.Skip("-run on this host needs qemu-x86_64")
			}
		}
		cmd := exec.Command(bin, "-target", "x86-64-linux", "-run", src)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("fern -run exit = %d, want 42", code)
		}
	})

}
