//go:build !linux && !darwin

package e2e

import (
	"runtime"
	"testing"
)

const xattrAbsentText = ""

func setUserXattr(t *testing.T, _, _, _ string) {
	t.Helper()
	t.Skipf("no extended attributes to set on %s", runtime.GOOS)
}

func hostUserXattr(t *testing.T, _, _ string) (string, bool) {
	t.Helper()
	t.Skipf("no extended attributes to read on %s", runtime.GOOS)
	return "", false
}
