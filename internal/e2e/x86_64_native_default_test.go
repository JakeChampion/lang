package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// x86QemuOrEmpty returns "" on a native amd64 host (binaries run directly)
// or the path to qemu-x86_64 on a cross host. Skips when neither applies.
func x86QemuOrEmpty(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return ""
	}
	for _, c := range []string{"qemu-x86_64", "qemu-x86_64-static"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	t.Skip("no qemu-x86_64 to run x86-64 binaries")
	return ""
}

func runX86Bin(qemu, binPath string, args ...string) *exec.Cmd {
	if qemu == "" {
		return exec.Command(binPath, args...)
	}
	return exec.Command(qemu, append([]string{binPath}, args...)...)
}

// The CLI defaults to the in-process pure-Go assembler+linker for
// -target x86-64-linux (no external toolchain), mirroring arm64. Exercises the
// full default path, the --run temp-binary path, and the -cc opt-out.
func TestX86_64NativeIsCLIDefault(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 42; }\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	t.Run("default_build_is_native", func(t *testing.T) {
		out := filepath.Join(dir, "prog.bin")
		// No -cc: must build with no external assembler/linker.
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
		// On a native amd64 host the CLI execs directly; on a cross host it
		// shells out to qemu-x86_64, so skip there if it's unavailable.
		if runtime.GOARCH != "amd64" {
			if _, err := exec.LookPath("qemu-x86_64"); err != nil {
				t.Skip("--run on this host needs qemu-x86_64")
			}
		}
		cmd := exec.Command(bin, "-target", "x86-64-linux", "--run", src)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("fern --run exit = %d, want 42", code)
		}
	})

	t.Run("cc_opts_out_to_external", func(t *testing.T) {
		// A failing -cc must make the build fail, proving the default path
		// does NOT shell out to an external assembler/linker.
		out := filepath.Join(dir, "prog_cc.bin")
		if err := exec.Command(bin, "-target", "x86-64-linux", "-cc", "/bin/false", "-o", out, src).Run(); err == nil {
			t.Errorf("expected build to fail when -cc points at a failing linker, but it succeeded")
		}
	})
}
