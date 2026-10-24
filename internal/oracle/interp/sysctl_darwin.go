//go:build darwin

package interp

import (
	"syscall"
	"unsafe"
)

func sysctlMIB(mib []int32) ([]byte, error) {
	if len(mib) == 0 {
		return nil, syscall.EINVAL
	}
	var size uintptr
	if _, _, e := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)), 0, uintptr(unsafe.Pointer(&size)), 0, 0); e != 0 {
		return nil, e
	}
	buf := make([]byte, size+1)
	if _, _, e := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, 0); e != 0 {
		return nil, e
	}
	return buf[:size], nil
}
