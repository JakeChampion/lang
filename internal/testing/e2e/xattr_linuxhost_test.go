//go:build linux

package e2e

import (
	"syscall"
	"testing"
)

// xattrAbsentText is strerror(ENODATA), what Linux answers for an
// attribute that is not there.
const xattrAbsentText = "No data available"

// setUserXattr sets a user.* attribute. A filesystem that refuses user
// attributes leaves nothing to read back, so the test cannot run there.
func setUserXattr(t *testing.T, path, name, value string) {
	t.Helper()
	if err := syscall.Setxattr(path, name, []byte(value), 0); err != nil {
		if err == syscall.ENOTSUP {
			t.Skipf("%s: the filesystem refuses user extended attributes", path)
		}
		t.Fatalf("setxattr %s %s: %v", path, name, err)
	}
}

// hostUserXattr reads an attribute back through the host, so a probe's write
// is checked by something other than the probe; ok is false when it is absent.
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
