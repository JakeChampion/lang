//go:build unix

package interp

import "syscall"

// getProcPriority is getpriority(PRIO_PROCESS, 0): this process's nice
// value. It cannot fail for the caller's own process, so the errno
// syscall.Getpriority would report is discarded rather than folded into
// the answer — -1 is a legal nice value and would be indistinguishable
// from the error return otherwise.
func getProcPriority() int {
	n, err := syscall.Getpriority(syscall.PRIO_PROCESS, 0)
	if err != nil {
		return 0
	}
	// Linux's getpriority(2) libc wrapper already maps the kernel's
	// biased 1..40 back to 20..-19; Go's syscall does NOT, so the bias
	// is undone here.
	return 20 - n
}

// setProcPriority is setpriority(PRIO_PROCESS, 0, nice).
func setProcPriority(nice int) error {
	return syscall.Setpriority(syscall.PRIO_PROCESS, 0, nice)
}
