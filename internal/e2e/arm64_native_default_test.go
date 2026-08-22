package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// buildFernCLI compiles cmd/fern to a temp binary. The Go build cache
// makes the repeated build essentially free.
func buildFernCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}
	return bin
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

// The CLI defaults to the in-process pure-Go assembler+linker for
// -target arm64-linux (no external toolchain). This exercises the full default
// path end to end, the --run temp-binary path (which must be chmod'd
// executable), and the -cc opt-out to an external linker.
func TestArm64NativeIsCLIDefault(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	// Self-contained — no stdlib, so the only thing exercised is codegen
	// + the native assembler/linker.
	if err := os.WriteFile(src, []byte("function main(): i32 { return 42; }\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	// The emulator lookup belongs in the legs that RUN a binary. Hoisted to
	// the top it skipped the whole test on an emulator-less host, taking the
	// exec-bit and -cc checks — neither of which runs anything — with it.
	t.Run("default_build_is_native", func(t *testing.T) {
		qemu := arm64QemuOrEmpty(t)
		out := filepath.Join(dir, "prog.bin")
		// No -cc: must build with no external assembler/linker on PATH.
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

	t.Run("cc_opts_out_to_external", func(t *testing.T) {
		// Passing -cc routes through that external linker. A linker that
		// always fails must therefore make the build fail — proving the
		// default path is NOT using -cc.
		out := filepath.Join(dir, "prog_cc.bin")
		if err := exec.Command(bin, "-target", "arm64-linux", "-cc", "/bin/false", "-o", out, src).Run(); err == nil {
			t.Errorf("expected build to fail when -cc points at a failing linker, but it succeeded")
		}
	})
}
