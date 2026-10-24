//go:build !linux && !darwin

package e2e

import (
	"runtime"
	"testing"
)

func hostFsFacts(t *testing.T, _ string) (blockSize, nameMax, pathMax int64) {
	t.Helper()
	t.Skipf("the statfs probe compares against statfs(2); %s has none here", runtime.GOOS)
	return 0, 0, 0
}

func hostFsIdentity(t *testing.T, _ string) (fsType, fsid, fragSize int64) {
	t.Helper()
	t.Skipf("the statfs probe compares against statfs(2); %s has none here", runtime.GOOS)
	return 0, 0, 0
}
