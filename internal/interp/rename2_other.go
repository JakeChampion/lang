//go:build !linux && !darwin

package interp

import "syscall"

// ENOSYS rather than a plain rename: either fallback would drop the
// condition the caller asked the kernel to hold.
func renameNoReplace(string, string) error { return syscall.ENOSYS }

func renameExchangeNames(string, string) error { return syscall.ENOSYS }
