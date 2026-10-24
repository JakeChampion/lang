//go:build linux

package interp

import (
	"runtime"
	"syscall"
	"unsafe"
)

const (
	renameNoreplace = 1
	renameExchange  = 2
)

func renameat2(from, to string, flags uintptr) error {
	var sysno uintptr
	switch runtime.GOARCH {
	case "amd64":
		sysno = 316
	case "arm64":
		sysno = 276
	default:
		return syscall.ENOSYS
	}
	f, err := syscall.BytePtrFromString(from)
	if err != nil {
		return err
	}
	t, err := syscall.BytePtrFromString(to)
	if err != nil {
		return err
	}
	dirfd := atFdcwd
	_, _, errno := syscall.Syscall6(sysno, uintptr(dirfd), uintptr(unsafe.Pointer(f)),
		uintptr(dirfd), uintptr(unsafe.Pointer(t)), flags, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func renameNoReplace(from, to string) error { return renameat2(from, to, renameNoreplace) }

func renameExchangeNames(a, b string) error { return renameat2(a, b, renameExchange) }
