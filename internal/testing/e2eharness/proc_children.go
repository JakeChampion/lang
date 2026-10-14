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

// ChildPIDs lists pid's direct children. It reads
// /proc/PID/task/PID/children, which a kernel built without
// CONFIG_CHECKPOINT_RESTORE does not provide; there it scans every
// process's PPid instead. A missing parent, unreadable metadata and a
// malformed record are errors either way.
func ChildPIDs(pid int) ([]string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	if err == nil {
		return strings.Fields(string(b)), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err != nil {
		return nil, fmt.Errorf("process %d: %w", pid, err)
	}
	return childrenByPPid(os.DirFS("/proc"), pid)
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
	sort.Ints(kids)
	out := make([]string, len(kids))
	for i, k := range kids {
		out[i] = strconv.Itoa(k)
	}
	return out, nil
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
