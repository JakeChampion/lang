//go:build !unix

package interp

import "syscall"

// Hosts without process groups must report that the operation is unavailable.
func hostSetProcessGroup(int, int) error { return syscall.ENOSYS }
