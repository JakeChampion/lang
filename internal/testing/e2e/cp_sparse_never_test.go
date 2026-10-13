//go:build linux

package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// sysCopyFileRange is copy_file_range(2)'s number on the host, which the
// stdlib syscall package does not name.
func sysCopyFileRange(t *testing.T) uintptr {
	switch runtime.GOARCH {
	case "amd64":
		return 326
	case "arm64":
		return 285
	}
	t.Skipf("no copy_file_range number for %s", runtime.GOARCH)
	return 0
}

// `--sparse=never` is never handed to copy_file_range, which may keep a hole
// the copy must not have: with every copy_file_range failing EIO, a hard
// error under the default, both a dense and a sparse source still copy.
func TestCpSparseNeverIsNotOffloaded(t *testing.T) {
	dir := t.TempDir()
	dense := bytes.Repeat([]byte("dense\n"), 50000)
	if err := os.WriteFile(filepath.Join(dir, "dense"), dense, 0o644); err != nil {
		t.Fatal(err)
	}
	sparse := make([]byte, 3<<20)
	copy(sparse, "head")
	copy(sparse[len(sparse)-4:], "tail")
	f, err := os.Create(filepath.Join(dir, "sparse"))
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{0, int64(len(sparse) - 4)} {
		if _, err := f.WriteAt(sparse[at:at+4], at); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	runner := e2eharness.X86_64Runner(t)
	bin := e2eharness.CompileSelfHostFile(t, e2eharness.TargetX86_64Linux, e2eharness.RepoPath("coreutils", "cp.fern"), nil)
	nr := sysCopyFileRange(t)
	for _, c := range []struct {
		src  string
		want []byte
	}{{"dense", dense}, {"sparse", sparse}} {
		argv := append(e2eharness.RunX86_64Bin(runner, bin).Args, "--sparse=never", c.src, c.src+".out")
		cmd := underSeccompErrno(t, dir, syscall.EIO, []uintptr{nr}, argv...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cp --sparse=never %s: %v\n%s", c.src, err, out)
		}
		got, err := os.ReadFile(filepath.Join(dir, c.src+".out"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, c.want) {
			t.Errorf("cp --sparse=never %s: copy differs from the source", c.src)
		}
	}
	// The default sparse mode does offload, so the same filter is an error
	// there; that keeps this test honest about the filter reaching cp at all.
	// No clone, which a reflink-capable filesystem would answer first.
	argv := append(e2eharness.RunX86_64Bin(runner, bin).Args, "--reflink=never", "dense", "dense.auto")
	if out, err := underSeccompErrno(t, dir, syscall.EIO, []uintptr{nr}, argv...).CombinedOutput(); err == nil {
		t.Errorf("cp with copy_file_range failing EIO succeeded:\n%s", out)
	}
}
