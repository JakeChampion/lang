//go:build linux

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestCpCloneRefusalHoldsForTheTree(t *testing.T) {
	dir := t.TempDir()
	const files = 5
	for i := 0; i < files; i++ {
		if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "src", fmt.Sprintf("f%d", i)), []byte("data\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runner := e2eharness.X86_64Runner(t)
	bin := e2eharness.CompileSelfHostFile(t, e2eharness.TargetX86_64Linux, e2eharness.RepoPath("coreutils", "cp.fern"), nil)
	argv := append(e2eharness.RunX86_64Bin(runner, bin).Args, "-a", "src", "dst")
	cmd := underSeccompErrno(t, dir, syscall.EPERM, []uintptr{syscall.SYS_IOCTL, syscall.SYS_UNLINKAT}, argv...)
	out, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 1 {
		t.Fatalf("cp exited %d, want 1:\n%s", code, out)
	}
	if n := strings.Count(string(out), "cannot create regular file"); n != 1 {
		t.Errorf("%d files refused, want only the first clone's:\n%s", n, out)
	}
	copied := 0
	for i := 0; i < files; i++ {
		got, err := os.ReadFile(filepath.Join(dir, "dst", fmt.Sprintf("f%d", i)))
		if err == nil && string(got) == "data\n" {
			copied++
		}
	}
	if copied != files-1 {
		t.Errorf("%d files copied, want %d", copied, files-1)
	}
}
