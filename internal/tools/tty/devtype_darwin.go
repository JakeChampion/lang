//go:build darwin

package tty

import (
	"syscall"
	"unsafe"
)

const (
	fiodType = 0x4004667a
	dTTY     = 3
)

func deviceTypeIsTTY(fd int) (isTTY, ok bool) {
	var typ int32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), fiodType, uintptr(unsafe.Pointer(&typ)))
	if errno != 0 {
		return false, false
	}
	return typ == dTTY, true
}
