package e2eharness

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
)

// ChildPIDs lists pid's direct children, in pid order. Each thread's
// /proc/PID/task/TID/children names the children that thread forked, so
// it reads every thread's; a kernel built without
// CONFIG_CHECKPOINT_RESTORE provides none, and there it scans every
// process's PPid instead. A missing parent, unreadable metadata and a
// malformed record are errors either way.
func ChildPIDs(pid int) ([]string, error) {
	tasks, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
	if err != nil {
		return nil, fmt.Errorf("process %d: %w", pid, err)
	}
	var kids []int
	for _, task := range tasks {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/children", pid, task.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			if _, serr := os.Stat(fmt.Sprintf("/proc/%d/task/%s", pid, task.Name())); serr == nil {
				return childrenByPPid(os.DirFS("/proc"), pid)
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, f := range strings.Fields(string(b)) {
			n, err := strconv.Atoi(f)
			if err != nil {
				return nil, fmt.Errorf("/proc/%d/task/%s/children: %q: %w", pid, task.Name(), f, err)
			}
			kids = append(kids, n)
		}
	}
	return pidStrings(kids), nil
}

// childrenByPPid scans proc, a /proc tree, for the processes whose
// status names pid as their parent, in pid order. A process that exits
// between the listing and the read is skipped.
func childrenByPPid(proc fs.FS, pid int) ([]string, error) {
	entries, err := fs.ReadDir(proc, ".")
	if err != nil {
		return nil, err
	}
	var kids []int
	for _, e := range entries {
		n, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		b, err := fs.ReadFile(proc, e.Name()+"/status")
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		ppid, err := statusPPid(string(b))
		if err != nil {
			return nil, fmt.Errorf("/proc/%s/status: %w", e.Name(), err)
		}
		if ppid == pid {
			kids = append(kids, n)
		}
	}
	return pidStrings(kids), nil
}

func pidStrings(pids []int) []string {
	sort.Ints(pids)
	out := make([]string, len(pids))
	for i, p := range pids {
		out[i] = strconv.Itoa(p)
	}
	return out
}

// statusPPid reads the PPid line of a /proc/PID/status record.
func statusPPid(status string) (int, error) {
	for _, line := range strings.Split(status, "\n") {
		if v, ok := strings.CutPrefix(line, "PPid:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return 0, fmt.Errorf("PPid %q: %w", strings.TrimSpace(v), err)
			}
			return n, nil
		}
	}
	return 0, errors.New("no PPid line")
}
