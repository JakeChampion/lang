//go:build !unix

package interp

import "syscall"

func chownAt(string, int, int, bool) error { return syscall.ENOSYS }
