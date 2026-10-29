//go:build !linux

package e2eharness

import "syscall"

func pinToCPU(cpu int) error {
	return syscall.ENOTSUP
}
