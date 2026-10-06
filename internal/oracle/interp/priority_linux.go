//go:build linux

package interp

import "syscall"

// getProcPriority is getpriority(PRIO_PROCESS, 0): this process's nice
// value.
//
// Linux's kernel returns the value BIASED by 20 — nice 19 arrives as 1 —
// so that a success is never negative, and every libc wrapper undoes it.
// Go's syscall.Getpriority is the RAW syscall here, so the bias is
// undone below. It cannot fail for the caller's own process, so the
// errno is discarded rather than folded into the answer: -1 is a legal
// nice value and would be indistinguishable from the error return.
func getProcPriority() int {
	n, err := syscall.Getpriority(syscall.PRIO_PROCESS, 0)
	if err != nil {
		return 0
	}
	return 20 - n
}

// setProcPriority is setpriority(PRIO_PROCESS, 0, nice). No bias on this
// side: only the READ is biased.
func setProcPriority(nice int) error {
	return syscall.Setpriority(syscall.PRIO_PROCESS, 0, nice)
}
