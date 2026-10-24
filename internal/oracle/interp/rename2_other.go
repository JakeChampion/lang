//go:build !linux && !darwin

package interp

import "syscall"

func renameNoReplace(string, string) error { return syscall.ENOSYS }

func renameExchangeNames(string, string) error { return syscall.ENOSYS }
