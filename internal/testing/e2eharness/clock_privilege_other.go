//go:build !linux

package e2eharness

import (
	"os"
	"os/exec"
	"testing"
)

func WithoutClockPrivilege(t testing.TB, cmd *exec.Cmd) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root, which may set the clock, and there is no user namespace here to run the case without that privilege")
	}
}
