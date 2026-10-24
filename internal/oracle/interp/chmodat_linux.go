//go:build linux

package interp

import (
	"syscall"
	"unsafe"
)

const sysFchmodat2 = 452

func chmodAt(path string, mode uint32, follow bool) error {
	if follow {
		return syscall.Chmod(path, mode)
	}
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	dirfd := atFdcwd
	_, _, errno := syscall.Syscall6(sysFchmodat2, uintptr(dirfd),
		uintptr(unsafe.Pointer(p)), uintptr(mode), atSymlinkNofollow, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
