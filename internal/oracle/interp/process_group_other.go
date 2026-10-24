//go:build !unix

package interp

import "syscall"

func hostSetProcessGroup(int, int) error { return syscall.ENOSYS }
