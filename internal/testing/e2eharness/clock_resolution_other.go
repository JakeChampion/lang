//go:build !linux

package e2eharness

import (
	"runtime"
	"testing"
)

func HostClockResolution(t testing.TB) int64 {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skipf("no known wall-clock resolution to compare against on %s", runtime.GOOS)
	}
	return 1000
}
