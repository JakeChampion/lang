//go:build linux

package interp

import (
	"runtime"
	"syscall"
)

func getProcPriority() int {
	runtime.LockOSThread()
	n, err := syscall.Getpriority(syscall.PRIO_PROCESS, 0)
	if err != nil {
		return 0
	}
	return 20 - n
}

func setProcPriority(nice int) error {
	runtime.LockOSThread()
	return syscall.Setpriority(syscall.PRIO_PROCESS, 0, nice)
}
