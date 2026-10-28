package interp

import "syscall"

// prctlQuery answers a read-only prctl(2) option with the kernel's result,
// or the negated errno.
func prctlQuery(option int) int64 {
	r, _, e := syscall.RawSyscall6(syscall.SYS_PRCTL, uintptr(option), 0, 0, 0, 0, 0)
	if e != 0 {
		return -int64(e)
	}
	return int64(r)
}
