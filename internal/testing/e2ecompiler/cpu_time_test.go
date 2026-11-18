package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// cpuTime runs the command and gives the CPU time of its process and of the
// children that it waited for. The wall time also counts the time that the
// other workers on the machine take, so a growth test reads this time.
func cpuTime(t *testing.T, name string, args ...string) time.Duration {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", filepath.Base(name), args, err, out)
	}
	return cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()
}
