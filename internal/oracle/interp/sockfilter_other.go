//go:build !linux

package interp

import "syscall"

func hostSteerByCPU(fd int) error {
	return syscall.ENOTSUP
}
