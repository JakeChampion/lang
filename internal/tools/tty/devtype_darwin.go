//go:build darwin

package tty

import (
	"syscall"
	"unsafe"
)

// FIODTYPE, and the device type it reports for a terminal.
const (
	fiodType = 0x4004667a
	dTTY     = 3
)

// deviceTypeIsTTY answers as Darwin's isatty(3) does first: whether the
// device type is D_TTY. A pseudo-terminal master is one, though it refuses
// TIOCGETA. ok is false when the ioctl itself fails.
func deviceTypeIsTTY(fd int) (isTTY, ok bool) {
	var typ int32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), fiodType, uintptr(unsafe.Pointer(&typ)))
	if errno != 0 {
		return false, false
	}
	return typ == dTTY, true
}
