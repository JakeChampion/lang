//go:build !linux && !darwin

package interp

import "syscall"

func mknodAt(string, uint32, uint32, uint32) error { return syscall.ENOSYS }
