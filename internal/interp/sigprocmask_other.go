//go:build !linux && !darwin

package interp

import "syscall"

// sigprocmaskSwap on a platform whose sigprocmask syscall number this
// package does not carry: ENOSYS, the same answer proc_fork refuses with,
// rather than a silent no-op that blocks nothing.
func sigprocmaskSwap(how uintptr, mask int64) (int64, syscall.Errno) {
	return 0, syscall.ENOSYS
}