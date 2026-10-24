//go:build darwin

package interp

import (
	"syscall"
	"unsafe"
)

func sigprocmaskSwap(how uintptr, mask int64) (int64, syscall.Errno) {
	var newset, oldset [32]byte
	*(*int64)(unsafe.Pointer(&newset[0])) = mask
	_, _, errno := syscall.Syscall6(syscall.SYS_SIGPROCMASK, how+1,
		uintptr(unsafe.Pointer(&newset[0])), uintptr(unsafe.Pointer(&oldset[0])), 0, 0, 0)
	if errno != 0 {
		return 0, errno
	}
	return *(*int64)(unsafe.Pointer(&oldset[0])), 0
}
