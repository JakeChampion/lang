//go:build !linux && !darwin

package interp

import "syscall"

func chmodAt(string, uint32, bool) error { return syscall.ENOSYS }
