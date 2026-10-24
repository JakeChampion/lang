//go:build !linux

package e2ecompiler

import (
	"runtime"
	"testing"
)

func selfHostUtsname(t *testing.T) [5]string {
	t.Helper()
	t.Skipf("the utsname probe reads uname(2); %s is not it", runtime.GOOS)
	return [5]string{}
}
