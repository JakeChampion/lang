//go:build !linux && !darwin

package interp

import "syscall"

func cloneFile(src, dest string) error {
	return syscall.ENOTSUP
}
