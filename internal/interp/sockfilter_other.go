//go:build !linux

package interp

import "syscall"

// hostSteerByCPU: SO_ATTACH_REUSEPORT_CBPF is Linux's; every other host
// answers -ENOTSUP, as the native runtimes do on Darwin.
func hostSteerByCPU(fd int) error {
	return syscall.ENOTSUP
}
