//go:build darwin

package interp

import (
	"syscall"
	"unsafe"
)

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
	dirfd := -2
	_, _, errno := syscall.Syscall6(sysRenameatxNp, uintptr(dirfd), uintptr(unsafe.Pointer(f)),
		uintptr(dirfd), uintptr(unsafe.Pointer(t)), flags, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func renameNoReplace(from, to string) error { return renameatxNp(from, to, 0x4) }

func renameExchangeNames(a, b string) error { return renameatxNp(a, b, 0x2) }
