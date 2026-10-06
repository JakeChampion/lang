//go:build darwin

package interp

import (
	"syscall"
	"unsafe"
)

// renameatxNp is renameatx_np(AT_FDCWD, from, AT_FDCWD, to, flags), BSD
// 488, issued directly: `syscall` has no binding for it.
func renameatxNp(from, to string, flags uintptr) error {
	const sysRenameatxNp = 488
	f, err := syscall.BytePtrFromString(from)
	if err != nil {
		return err
	}
	t, err := syscall.BytePtrFromString(to)
	if err != nil {
		return err
	}
	dirfd := -2 // AT_FDCWD
	_, _, errno := syscall.Syscall6(sysRenameatxNp, uintptr(dirfd), uintptr(unsafe.Pointer(f)),
		uintptr(dirfd), uintptr(unsafe.Pointer(t)), flags, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// RENAME_EXCL and RENAME_SWAP.
func renameNoReplace(from, to string) error { return renameatxNp(from, to, 0x4) }

func renameExchangeNames(a, b string) error { return renameatxNp(a, b, 0x2) }
