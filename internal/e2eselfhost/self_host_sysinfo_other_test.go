//go:build !linux

package e2eselfhost

import (
	"runtime"
	"testing"
)

// selfHostUtsname has nothing to read on a platform with no utsname
// record: the probe compares against the host's own kernel facts, which
// only exist on the uname(2) targets the cross-check was written for.
func selfHostUtsname(t *testing.T) [5]string {
	t.Helper()
	t.Skipf("the utsname probe reads uname(2); %s is not it", runtime.GOOS)
	return [5]string{}
}
