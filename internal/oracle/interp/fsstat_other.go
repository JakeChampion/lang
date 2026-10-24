//go:build !linux && !darwin

package interp

import "syscall"

func fsStatFields(string) (rawFsStat, error) { return rawFsStat{}, syscall.ENOSYS }
