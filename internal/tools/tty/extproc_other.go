//go:build !darwin

package tty

import "syscall"

func SetExtproc(fd int, _ bool) error {
	if fd < 0 {
		return syscall.EBADF
	}
	return syscall.ENOTTY
}
