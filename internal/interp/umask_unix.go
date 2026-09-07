//go:build unix

package interp

import "syscall"

// setUmask is umask(2): it installs `mask` as the process file-mode
// creation mask and answers the one it replaced, in a single step.
// POSIX has no read-only form, so `umask(umask(0))` is how a program
// reads the mask without changing it.
func setUmask(mask int) int {
	return syscall.Umask(mask)
}
