//go:build !linux && !darwin

package interp

import "syscall"

func raiseSelf(sig syscall.Signal) error { return syscall.ENOSYS }
