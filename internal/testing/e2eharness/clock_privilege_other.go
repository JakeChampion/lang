//go:build !linux

package e2eharness

import (
	"os"
	"os/exec"
	"testing"
)

// WithoutClockPrivilege makes cmd run unable to set the system clock. Off
// Linux there is no user namespace to drop the privilege into, so a root
// test process cannot run such a case safely and skips it; anyone else is
// already refused.
func WithoutClockPrivilege(t testing.TB, cmd *exec.Cmd) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root, which may set the clock, and there is no user namespace here to run the case without that privilege")
	}
}
