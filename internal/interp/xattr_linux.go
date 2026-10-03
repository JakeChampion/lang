//go:build linux

package interp

import (
	"syscall"
	"unsafe"
)

// xattrSizeMax is XATTR_SIZE_MAX, the largest value Linux stores, so one
// buffer of it always holds the whole answer.
const xattrSizeMax = 65536

// getxattrBytes is getxattr(2), or lgetxattr(2) when follow is false.
func getxattrBytes(path, name string, follow bool) ([]byte, error) {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return nil, err
	}
	n, err := syscall.BytePtrFromString(name)
	if err != nil {
		return nil, err
	}
	sysno := uintptr(syscall.SYS_GETXATTR)
	if !follow {
		sysno = syscall.SYS_LGETXATTR
	}
	buf := make([]byte, xattrSizeMax)
	r, _, errno := syscall.Syscall6(sysno, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(n)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0)
	if errno != 0 {
		return nil, errno
	}
	return buf[:r], nil
}
