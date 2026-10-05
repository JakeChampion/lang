package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// buildFernCLI compiles cmd/fern to a temp binary that compiles through the
// current self-host compiler.
func buildFernCLI(t *testing.T) string {
	t.Helper()
	return e2eharness.FernCLI(t)
}

// arm64QemuOrEmpty returns "" on a native arm64 Linux host (binaries run
// directly) or the path to qemu-aarch64 on a cross host. Skips when
// neither applies.
func arm64QemuOrEmpty(t *testing.T) string {
	t.Helper()
	runner, ok := arm64Runner()
	if !ok {
		t.Skip("no qemu-aarch64 to run arm64 binaries")
	}
	return runner
}

// arm64Runner is arm64QemuOrEmpty without the skip: ("", true) on a native
// arm64 Linux host, (qemuPath, true) on a cross host with an emulator, and
// ("", false) when there is no way to run an arm64 binary at all. Callers that
// want a missing emulator to be a FAILURE rather than a skip need the third
// case as a value.
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

// `fern -target arm64-linux` builds with no external toolchain. This
// exercises the default path end to end and the --run temp-binary path (which
// must be chmod'd executable).
func TestArm64NativeIsCLIDefault(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	// Self-contained — no stdlib, so the only thing exercised is codegen
	// + the native assembler/linker.
	if err := os.WriteFile(src, []byte("function main(): i32 { return 42; }\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	t.Run("default_build_is_native", func(t *testing.T) {
		qemu := arm64QemuOrEmpty(t)
		out := filepath.Join(dir, "prog.bin")
		// Must build with no external assembler/linker on PATH.
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
		// The CLI execs the binary DIRECTLY when the target matches the host
		// arch (cmd/fern's runIt path) and only shells out to qemu-aarch64 for
		// the cross case, so this needs the emulator to EXIST but not the path.
		arm64QemuOrEmpty(t)
		cmd := exec.Command(bin, "-target", "arm64-linux", "--run", src)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("fern --run exit = %d, want 42", code)
		}
	})

}
