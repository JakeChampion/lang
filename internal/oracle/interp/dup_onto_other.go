//go:build !linux && !darwin

package interp

import "syscall"

func hostDupOnto(int, int) error { return syscall.ENOSYS }
