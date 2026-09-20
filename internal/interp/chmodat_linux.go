//go:build linux

package interp

import (
	"syscall"
	"unsafe"
)

// sysFchmodat2 is fchmodat2(2), 452 on every Linux architecture. Not in
// `syscall`, whose Fchmodat rewrites an ENOSYS from this call into
// EOPNOTSUPP; the builtin reports the kernel's own answer.
const sysFchmodat2 = 452

// chmodAt sets the permission bits of `path`. `follow` true is chmod(2).
// False is fchmodat2 with AT_SYMLINK_NOFOLLOW, which for a symlink is the
// kernel's EOPNOTSUPP — Linux keeps no mode on one — and ENOSYS where the
// syscall itself is missing. Neither is hidden.
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
