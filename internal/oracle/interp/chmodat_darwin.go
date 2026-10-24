//go:build darwin

package interp

import (
	"syscall"
	"unsafe"
)

func chmodAt(path string, mode uint32, follow bool) error {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	const (
		sysFchmodat       = 467
		atSymlinkNofollow = 0x20
	)
	var flags uintptr
	if !follow {
		flags = atSymlinkNofollow
	}
	dirfd := -2
	_, _, errno := syscall.Syscall6(sysFchmodat, uintptr(dirfd),
		uintptr(unsafe.Pointer(p)), uintptr(mode), flags, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
