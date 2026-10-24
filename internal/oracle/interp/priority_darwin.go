//go:build darwin

package interp

import "syscall"

func getProcPriority() int {
	n, err := syscall.Getpriority(syscall.PRIO_PROCESS, 0)
	if err != nil {
		return 0
	}
	return n
}

func setProcPriority(nice int) error {
	return syscall.Setpriority(syscall.PRIO_PROCESS, 0, nice)
}
