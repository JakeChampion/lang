package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A heap arena whose mmap fails is reported as that, with the errno and
// ExitArenaUnmapped, not as exhaustion's ExitArenaExhausted (#12911). The
// address space is capped below the arena's 16 GiB: natively with
// `ulimit -v`, under qemu-user with QEMU_RESERVED_VA, which bounds the guest's
// address space where RLIMIT_AS would bound qemu itself.
func TestSelfHostArenaUnmappedExit(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	prog := "function main(): i32 {\n  let p: usize = __alloc(64);\n  if (p == 0) {\n    return 9;\n  }\n  return 0;\n}\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = "fern: could not map the heap arena (errno 12)\n"
	capped := func(bin string) *exec.Cmd {
		return exec.Command("sh", "-c", `ulimit -v 1048576 && exec "$0"`, bin)
	}
	check := func(t *testing.T, cmd *exec.Cmd) {
		var errb bytes.Buffer
		cmd.Stderr = &errb
		_ = cmd.Run()
		code := cmd.ProcessState.ExitCode()
		first, _, _ := bytes.Cut(errb.Bytes(), []byte("\n"))
		if code != e2eharness.ExitArenaUnmapped || string(first)+"\n" != want {
			t.Errorf("exit %d, stderr %q; want %d and %q", code, errb.String(), e2eharness.ExitArenaUnmapped, want)
		}
	}
	t.Run("x86-64-linux", func(t *testing.T) {
		if len(cli.runner) > 0 {
			t.Skip("x86-64 runs under an emulator here; ulimit -v would cap the emulator")
		}
		check(t, capped(cli.x86Binary(t, src)))
	})
	t.Run("arm64-linux", func(t *testing.T) {
		_, qemu := arm64Tooling(t)
		bin := cli.arm64Binary(t, src)
		if qemu == "" {
			check(t, capped(bin))
			return
		}
		cmd := runArm64Bin(qemu, bin)
		cmd.Env = append(os.Environ(), "QEMU_RESERVED_VA=0x100000000")
		check(t, cmd)
	})
}
