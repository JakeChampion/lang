//go:build linux

package interp

import (
	"syscall"
	"unsafe"
)

// sigprocmaskSwap is rt_sigprocmask(2): it applies `how` — already in
// Linux's numbering, which IS Fern's — to the mask and answers the one it
// replaced. The sigset is the 8-byte word the i64 mask convention writes,
// and sigsetsize 8 is what the kernel demands for that layout.
func sigprocmaskSwap(how uintptr, mask int64) (int64, syscall.Errno) {
	var newset, oldset [8]byte
	*(*int64)(unsafe.Pointer(&newset[0])) = mask
	_, _, errno := syscall.Syscall6(syscall.SYS_RT_SIGPROCMASK, how,
		uintptr(unsafe.Pointer(&newset[0])), uintptr(unsafe.Pointer(&oldset[0])), 8, 0, 0)
	if errno != 0 {
		return 0, errno
	}
	return *(*int64)(unsafe.Pointer(&oldset[0])), 0
}
