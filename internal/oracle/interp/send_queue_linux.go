//go:build linux

package interp

import (
	"syscall"
	"unsafe"
)

func hostSendQueued(fd int) (int, error) {
	var n int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCOUTQ, uintptr(unsafe.Pointer(&n))); e != 0 {
		return 0, e
	}
	return int(n), nil
}
