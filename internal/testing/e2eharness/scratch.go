package e2eharness

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func ProcessScratchDir(prefix string) (string, error) {
	removeDeadScratchDirs(prefix)
	return os.MkdirTemp("", fmt.Sprintf("%s-p%d-", prefix, os.Getpid()))
}

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

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}
