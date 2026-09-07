package e2eharness

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ProcessScratchDir returns a fresh temp dir for artifacts that must outlive
// an individual test but not the test process — a compiler binary shared by
// parallel subtests, a link cache. `t.TempDir()` cannot serve them: it is torn
// down at the end of the test that asked for it, taking the artifact out from
// under any sibling still running.
//
// Nothing removes such a dir when the process exits. `TestMain` is not an
// option: neither consuming package declares one, and the exits that matter
// most — a `SIGKILL`, a test-binary panic on the 10-minute timeout — run no
// deferred code anyway. So the dir names its creating pid instead, and each
// creation removes the dirs of processes that are gone (#8866).
//
// That bounds the set by the number of LIVE test processes rather than by
// elapsed time, which is both exact and self-healing. It matters because a
// full disk does not present as a full disk: it presents as files vanishing
// mid-test, and those get investigated as codegen defects.
//
// The dir is per-process rather than one shared path on purpose. Several
// worktrees of this repo build at once on one machine, and a shared path would
// hand one checkout another branch's compiler.
func ProcessScratchDir(prefix string) (string, error) {
	removeDeadScratchDirs(prefix)
	return os.MkdirTemp("", fmt.Sprintf("%s-p%d-", prefix, os.Getpid()))
}

// removeDeadScratchDirs deletes every `<prefix>-p<pid>-*` dir in the temp
// directory whose pid names no running process. Anything it cannot parse or
// cannot delete is left alone: this is opportunistic reclamation, and failing
// it must never fail the test that triggered it.
func removeDeadScratchDirs(prefix string) {
	tmp := os.TempDir()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, ok := scratchDirPid(e.Name(), prefix)
		if !ok || pid == os.Getpid() || processAlive(pid) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(tmp, e.Name()))
	}
}

// scratchDirPid extracts the pid from a `<prefix>-p<pid>-<random>` name.
func scratchDirPid(name, prefix string) (int, bool) {
	rest, ok := strings.CutPrefix(name, prefix+"-p")
	if !ok {
		return 0, false
	}
	digits, _, ok := strings.Cut(rest, "-")
	if !ok || digits == "" {
		return 0, false
	}
	pid, err := strconv.Atoi(digits)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// processAlive reports whether pid names a running process. Signal 0 performs
// the permission and existence checks without delivering anything; EPERM means
// the process exists under another user, which still counts as alive.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}
