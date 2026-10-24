//go:build linux || darwin

package interp

import "syscall"

const (
	sigKill = int64(syscall.SIGKILL)
	sigStop = int64(syscall.SIGSTOP)
)
