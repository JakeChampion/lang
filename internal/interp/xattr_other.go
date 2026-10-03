//go:build !linux && !darwin

package interp

import "syscall"

// getxattrBytes has no system call to make on this platform.
func getxattrBytes(string, string, bool) ([]byte, error) { return nil, syscall.ENOSYS }
