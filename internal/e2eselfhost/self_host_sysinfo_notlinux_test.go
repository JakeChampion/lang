//go:build !linux

package e2eselfhost

import (
	"runtime"
	"testing"
)

// selfHostUtsname has no uname(2) to read on non-Linux hosts; the
// assertion paths that compare against the host record skip there.
func selfHostUtsname(t *testing.T) [5]string {
	t.Helper()
	t.Skipf("the utsname probe reads uname(2); %s is not it", runtime.GOOS)
	return [5]string{}
}