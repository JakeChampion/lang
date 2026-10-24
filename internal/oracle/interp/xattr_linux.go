//go:build linux

package interp

import (
	"syscall"
	"unsafe"
)

const xattrSizeMax = 65536

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

func setxattrBytes(path, name string, value []byte, follow bool) error {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	n, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	sysno := uintptr(syscall.SYS_SETXATTR)
	if !follow {
		sysno = syscall.SYS_LSETXATTR
	}
	var v unsafe.Pointer
	if len(value) > 0 {
		v = unsafe.Pointer(&value[0])
	}
	_, _, errno := syscall.Syscall6(sysno, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(n)),
		uintptr(v), uintptr(len(value)), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
