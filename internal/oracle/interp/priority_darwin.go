//go:build darwin

package interp

import "syscall"

// getProcPriority is getpriority(PRIO_PROCESS, 0): this process's nice
// value.
//
// BSD returns that value DIRECTLY and reports failure through errno
// alone — there is no bias to undo, unlike Linux, and Go reaches it
// through libSystem's wrapper rather than the raw syscall. Applying
// Linux's `20 - n` here would report 20 for a default process.
//
// The -1 that means failure is also a legal nice value, which is why
// only the errno can tell them apart; for the caller's own process it
// cannot fail, so a failure reports the default.
func getProcPriority() int {
	n, err := syscall.Getpriority(syscall.PRIO_PROCESS, 0)
	if err != nil {
		return 0
	}
	return n
}

// setProcPriority is setpriority(PRIO_PROCESS, 0, nice).
func setProcPriority(nice int) error {
	return syscall.Setpriority(syscall.PRIO_PROCESS, 0, nice)
}
