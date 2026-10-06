//go:build !linux

package e2e

import (
	"runtime"
	"testing"
)

// hostUtsname has nothing to read on a platform with no utsname record:
// the probe compares against the host's own kernel facts, which only
// exist on the uname(2) targets the cross-check was written for. The
// interp's darwin fork (unameFields in sysinfo_darwin.go) reads the same
// sysctl names a darwin uname(2) would answer, so none of these legs
// assert a value on darwin.
func hostUtsname(t *testing.T) [5]string {
	t.Helper()
	t.Skipf("the utsname probe reads uname(2); %s is not it", runtime.GOOS)
	return [5]string{}
}
