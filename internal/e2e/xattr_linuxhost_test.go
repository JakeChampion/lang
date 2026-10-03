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
