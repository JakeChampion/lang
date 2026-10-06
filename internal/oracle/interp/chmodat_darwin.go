//go:build darwin

package interp

import (
	"syscall"
	"unsafe"
)

// chmodAt is fchmodat(AT_FDCWD, path, mode, follow ? 0 : AT_SYMLINK_NOFOLLOW),
// BSD 467, issued directly as setFileTimes does: `syscall` has no Fchmodat
// on Darwin. XNU keeps a mode on a symlink, so the nofollow form changes
// the link's own bits — lchmod(2) — rather than answering an errno.
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
	dirfd := -2 // AT_FDCWD
	_, _, errno := syscall.Syscall6(sysFchmodat, uintptr(dirfd),
		uintptr(unsafe.Pointer(p)), uintptr(mode), flags, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
