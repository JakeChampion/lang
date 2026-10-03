//go:build !linux && !darwin

package e2eselfhost

import (
	"runtime"
	"testing"
)

const selfHostXattrAbsentText = ""

func selfHostSetUserXattr(t *testing.T, _, _, _ string) {
	t.Helper()
	t.Skipf("no extended attributes to set on %s", runtime.GOOS)
}
