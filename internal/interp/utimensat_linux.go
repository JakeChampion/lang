//go:build linux

package interp

import (
	"syscall"
	"unsafe"
)

// utimeOmit is UTIME_OMIT, the tv_nsec value that tells utimensat(2) to
// leave that timestamp alone. utimeNow (UTIME_NOW) is its sibling and is
// deliberately unused: `set_file_times` takes a clock reading from the
// caller, so "now" is a value rather than a mode.
const utimeOmit = 1<<30 - 2

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
