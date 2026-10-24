//go:build linux

package interp

import (
	"syscall"
	"unsafe"
)

const (
	utimeOmit = 1<<30 - 2
	utimeNow  = 1<<30 - 1
)

const atSymlinkNofollow = 0x100

func setFileTimes(path string, times *[2]syscall.Timespec, nofollow bool) error {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	var flags uintptr
	if nofollow {
		flags = atSymlinkNofollow
	}
	dirfd := atFdcwd
	_, _, errno := syscall.Syscall6(syscall.SYS_UTIMENSAT, uintptr(dirfd),
		uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(times)), flags, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
