//go:build !linux

package e2eharness

import (
	"runtime"
	"testing"
)

// HostClockResolution is the tick of the wall clock on a Darwin host, the
// microsecond of gettimeofday; XNU has no clock_getres syscall to ask.
func HostClockResolution(t testing.TB) int64 {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skipf("no known wall-clock resolution to compare against on %s", runtime.GOOS)
	}
	return 1000
}
