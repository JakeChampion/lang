//go:build linux || darwin

package tty

import (
	"syscall"
	"unsafe"
)

func IsTerminal(fd int) bool {
	if fd < 0 {
		return false
	}
	if isTTY, ok := deviceTypeIsTTY(fd); ok {
		return isTTY
	}
	var t syscall.Termios
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), tcGetAttr, uintptr(unsafe.Pointer(&t)), 0, 0, 0)
	return errno == 0
}
