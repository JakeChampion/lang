//go:build linux

package e2eharness

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// capSysTime is CAP_SYS_TIME's bit in a capability set.
const capSysTime = 25

// WithoutClockPrivilege makes cmd run unable to set the system clock, so a
// case that asks to set it reaches the EPERM an ordinary user gets instead of
// moving the machine's clock. A process already without CAP_SYS_TIME is left
// alone; one holding it (root in a container) starts the child in a new user
// namespace mapping it to the same ids, so files read and write as before but
// the capability, which clock_settime checks in the initial namespace, is
// gone.
func WithoutClockPrivilege(t testing.TB, cmd *exec.Cmd) {
	t.Helper()
	if !holdsCapSysTime(t) {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWUSER
	cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{{ContainerID: os.Getuid(), HostID: os.Getuid(), Size: 1}}
	cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{{ContainerID: os.Getgid(), HostID: os.Getgid(), Size: 1}}
}

// holdsCapSysTime reads this process's effective capability set.
func holdsCapSysTime(t testing.TB) bool {
	t.Helper()
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatalf("read /proc/self/status to learn whether the clock can be set: %v", err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		if hex, ok := strings.CutPrefix(line, "CapEff:"); ok {
			bits, err := strconv.ParseUint(strings.TrimSpace(hex), 16, 64)
			if err != nil {
				t.Fatalf("parse %q: %v", line, err)
			}
			return bits&(1<<capSysTime) != 0
		}
	}
	t.Fatalf("/proc/self/status has no CapEff line")
	return false
}
