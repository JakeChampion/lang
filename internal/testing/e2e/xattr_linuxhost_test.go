//go:build linux

package e2e

import (
	"syscall"
	"testing"
)

const xattrAbsentText = "No data available"

func setUserXattr(t *testing.T, path, name, value string) {
	t.Helper()
	if err := syscall.Setxattr(path, name, []byte(value), 0); err != nil {
		if err == syscall.ENOTSUP {
			t.Skipf("%s: the filesystem refuses user extended attributes", path)
		}
		t.Fatalf("setxattr %s %s: %v", path, name, err)
	}
}

func hostUserXattr(t *testing.T, path, name string) (string, bool) {
	t.Helper()
	buf := make([]byte, 256)
	n, err := syscall.Getxattr(path, name, buf)
	if err == syscall.ENODATA {
		return "", false
	}
	if err != nil {
		t.Fatalf("getxattr %s %s: %v", path, name, err)
	}
	return string(buf[:n]), true
}
