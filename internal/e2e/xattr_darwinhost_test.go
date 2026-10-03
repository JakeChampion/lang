//go:build darwin

package e2e

import (
	"syscall"
	"testing"
	"unsafe"
)

// xattrAbsentText is strerror(ENOATTR), what Darwin answers for an
// attribute that is not there.
const xattrAbsentText = "Attribute not found"

// setUserXattr is setxattr(2), BSD 236: (path, name, value, size,
// position, options). `syscall` has no binding for it on Darwin.
func setUserXattr(t *testing.T, path, name, value string) {
	t.Helper()
	const sysSetxattr = 236
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := syscall.BytePtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	v := []byte(value)
	if _, _, errno := syscall.Syscall6(sysSetxattr, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(n)),
		uintptr(unsafe.Pointer(&v[0])), uintptr(len(v)), 0, 0); errno != 0 {
		t.Fatalf("setxattr %s %s: %v", path, name, errno)
	}
}
