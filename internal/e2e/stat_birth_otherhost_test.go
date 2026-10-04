//go:build !linux && !darwin

package e2e

import (
	"runtime"
	"testing"
)

func hostBirth(t *testing.T, _ string) (int64, int64) {
	t.Helper()
	t.Skipf("no stat(2) record to compare on %s", runtime.GOOS)
	return 0, 0
}

func hostDevs(t *testing.T, _ string) (int64, int64) {
	t.Helper()
	t.Skipf("no stat(2) record to compare on %s", runtime.GOOS)
	return 0, 0
}
