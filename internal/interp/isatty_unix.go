//go:build linux || darwin

package interp

import (
	"syscall"
	"unsafe"
)

// isattyFd reports whether fd refers to a terminal, by the same
// mechanism the native backends emit: a terminal-attribute ioctl that
// succeeds only on a tty. fstat + S_ISCHR is the cruder alternative and
// answers yes for /dev/null.
func isattyFd(fd int) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), tcGetAttr, uintptr(unsafe.Pointer(&t)), 0, 0, 0)
	return errno == 0
}
