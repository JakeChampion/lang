//go:build !linux && !darwin

package interp

import "syscall"

func getProcPriority() int { return 0 }

func setProcPriority(int) error { return syscall.ENOSYS }
