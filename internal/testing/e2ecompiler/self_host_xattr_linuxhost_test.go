//go:build linux

package e2ecompiler

import (
	"syscall"
	"testing"
)

const selfHostXattrAbsentText = "No data available"

func selfHostSetUserXattr(t *testing.T, path, name, value string) {
	t.Helper()
	if err := syscall.Setxattr(path, name, []byte(value), 0); err != nil {
		if err == syscall.ENOTSUP {
			t.Skipf("%s: the filesystem refuses user extended attributes", path)
		}
		t.Fatalf("setxattr %s %s: %v", path, name, err)
	}
}
