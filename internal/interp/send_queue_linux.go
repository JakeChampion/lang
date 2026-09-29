//go:build linux

package interp

import (
	"syscall"
	"unsafe"
)

// hostSendQueued is the bytes queued to send on a socket, unsent and
// unacknowledged alike: SIOCOUTQ, which is TIOCOUTQ on a socket.
func hostSendQueued(fd int) (int, error) {
	var n int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCOUTQ, uintptr(unsafe.Pointer(&n))); e != 0 {
		return 0, e
	}
	return int(n), nil
}
