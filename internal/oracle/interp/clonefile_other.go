//go:build !linux && !darwin

package interp

import "syscall"

// cloneFile has no host call to make.
func cloneFile(src, dest string) error {
	return syscall.ENOTSUP
}
