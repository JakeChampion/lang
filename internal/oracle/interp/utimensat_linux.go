//go:build linux

package interp

import (
	"syscall"
	"unsafe"
)

// utimeOmit and utimeNow are UTIME_OMIT and UTIME_NOW, the tv_nsec
// values that tell utimensat(2) to leave that timestamp alone or to
// write its own clock reading into it.
const (
	utimeOmit = 1<<30 - 2
	utimeNow  = 1<<30 - 1
)

// atSymlinkNofollow is AT_SYMLINK_NOFOLLOW. Architecture-independent on
// Linux, and not exported by `syscall`.
const atSymlinkNofollow = 0x100

// setFileTimes is utimensat(AT_FDCWD, path, times, flags).
//
// `syscall.UtimesNano` would carry the timespecs — UTIME_OMIT included —
// but it hard-codes a zero flags word, so it cannot express nofollow.
// The syscall's own number is exported, so this calls it directly rather
// than reaching for the unexported binding behind UtimesNano.
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
