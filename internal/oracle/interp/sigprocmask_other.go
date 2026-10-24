//go:build !linux && !darwin

package interp

import "syscall"

func sigprocmaskSwap(how uintptr, mask int64) (int64, syscall.Errno) {
	return 0, syscall.ENOSYS
}
