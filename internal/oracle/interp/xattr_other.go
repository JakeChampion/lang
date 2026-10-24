//go:build !linux && !darwin

package interp

import "syscall"

func getxattrBytes(string, string, bool) ([]byte, error) { return nil, syscall.ENOSYS }

func setxattrBytes(string, string, []byte, bool) error { return syscall.ENOSYS }
