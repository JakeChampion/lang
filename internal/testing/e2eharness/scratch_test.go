package e2eharness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// deadPid returns the pid of a process that has already exited and been
// reaped. Pids are handed out in increasing order, so one just released is
// not reused within the lifetime of this test.
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("spawning a throwaway process: %v", err)
	}
	return cmd.Process.Pid
}

// testPrefix keeps each test's dirs disjoint from every other run's, including
// the real `fern-e2e-bin` and `selfhost-bincache` dirs of a concurrent
// `go test`, so a sweep here can never reclaim something in use.
func testPrefix(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("fern-scratch-test-%d-%s", os.Getpid(), t.Name())
}

func mkScratch(t *testing.T, prefix string, pid int) string {
	t.Helper()
	dir, err := os.MkdirTemp("", fmt.Sprintf("%s-p%d-", prefix, pid))
	if err != nil {
		t.Fatalf("creating a scratch dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, "artifact"), []byte("x"), 0o600); err != nil {
		t.Fatalf("writing into the scratch dir: %v", err)
	}
	return dir
}

func exists(t *testing.T, dir string) bool {
	t.Helper()
	_, err := os.Stat(dir)
	return err == nil
}

func TestProcessScratchDirNamesItsPid(t *testing.T) {
	prefix := testPrefix(t)
	dir, err := ProcessScratchDir(prefix)
	if err != nil {
		t.Fatalf("ProcessScratchDir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	want := fmt.Sprintf("%s-p%d-", prefix, os.Getpid())
	if got := filepath.Base(dir); !strings.HasPrefix(got, want) {
		t.Errorf("scratch dir %q does not carry the creating pid: want prefix %q", got, want)
	}
	pid, ok := scratchDirPid(filepath.Base(dir), prefix)
	if !ok || pid != os.Getpid() {
		t.Errorf("its own name does not parse back: got pid=%d ok=%v, want %d", pid, ok, os.Getpid())
	}
}

// The whole point of the pid in the name: a dir whose process is gone is
// reclaimed, one whose process is alive is not. Without this, every test
// process leaks its scratch dir forever (#8866).
func TestProcessScratchDirReclaimsOnlyDeadSiblings(t *testing.T) {
	prefix := testPrefix(t)
	dead := mkScratch(t, prefix, deadPid(t))
	alive := mkScratch(t, prefix, os.Getpid())

	mine, err := ProcessScratchDir(prefix)
	if err != nil {
		t.Fatalf("ProcessScratchDir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(mine) })

	if exists(t, dead) {
		t.Errorf("a scratch dir whose process has exited survived: %s", dead)
	}
	if !exists(t, alive) {
		t.Errorf("a scratch dir belonging to a live process was reclaimed: %s", alive)
	}
	if !exists(t, mine) {
		t.Errorf("the dir just created was reclaimed: %s", mine)
	}
}

// A sweep that reached past its own prefix would delete another harness's
// cache — or an unrelated program's temp dir — the moment two of them ran.
func TestRemoveDeadScratchDirsStaysWithinItsPrefix(t *testing.T) {
	prefix := testPrefix(t)
	other := testPrefix(t) + "-other"
	pid := deadPid(t)

	mine := mkScratch(t, prefix, pid)
	theirs := mkScratch(t, other, pid)

	removeDeadScratchDirs(prefix)

	if exists(t, mine) {
		t.Errorf("a dead dir under the swept prefix survived: %s", mine)
	}
	if !exists(t, theirs) {
		t.Errorf("the sweep reached a dir under a different prefix: %s", theirs)
	}
}

func TestScratchDirPid(t *testing.T) {
	const prefix = "fern-e2e-bin"
	cases := []struct {
		name string
		in   string
		pid  int
		ok   bool
	}{
		{"well formed", "fern-e2e-bin-p1234-987654321", 1234, true},
		{"a different prefix", "selfhost-bincache-p1234-987654321", 0, false},
		{"the bare prefix", "fern-e2e-bin", 0, false},
		{"the old unpidded shape", "fern-e2e-bin-987654321", 0, false},
		{"no random segment", "fern-e2e-bin-p1234", 0, false},
		{"an empty pid", "fern-e2e-bin-p-987654321", 0, false},
		{"a non-numeric pid", "fern-e2e-bin-pabc-987654321", 0, false},
		{"a negative pid", "fern-e2e-bin-p-12-987654321", 0, false},
		{"a longer prefix that merely starts the same", "fern-e2e-binary-p1234-9", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pid, ok := scratchDirPid(c.in, prefix)
			if ok != c.ok || pid != c.pid {
				t.Errorf("scratchDirPid(%q) = (%d, %v), want (%d, %v)", c.in, pid, ok, c.pid, c.ok)
			}
		})
	}
}

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("processAlive says this very process is not running")
	}
	if pid := deadPid(t); processAlive(pid) {
		t.Errorf("processAlive says an exited process (pid %d) is still running", pid)
	}
}
