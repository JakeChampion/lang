//go:build darwin

package e2eharness

import (
	"syscall"
	"testing"
	"unsafe"
)

// xattrAbsentText is strerror(ENOATTR), what Darwin answers for an
// attribute that is not there.
const xattrAbsentText = "Attribute not found"

// seedXattrBytesValue is setxattr(2), BSD 236: (path, name, value, size,
// position, options). `syscall` has no binding for it on Darwin.
func seedXattrBytesValue(t *testing.T, path, name, value string) {
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
	var data unsafe.Pointer
	if len(v) > 0 {
		data = unsafe.Pointer(&v[0])
	}
	if _, _, errno := syscall.Syscall6(sysSetxattr, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(n)),
		uintptr(data), uintptr(len(v)), 0, 0); errno != 0 {
		t.Fatalf("setxattr %s %s: %v", path, name, errno)
	}
}

// readXattrBytesValue is getxattr(2), BSD 234, read back through the host so a
// probe's write is checked by something other than the probe; ok is false
// when the attribute is absent.
func readXattrBytesValue(t *testing.T, path, name string) (string, bool) {
	t.Helper()
	const sysGetxattr = 234
	const enoattr = 93
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := syscall.BytePtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	r, _, errno := syscall.Syscall6(sysGetxattr, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(n)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0)
	if errno == enoattr {
		return "", false
	}
	if errno != 0 {
		t.Fatalf("getxattr %s %s: %v", path, name, errno)
	}
	return string(buf[:r]), true
}
