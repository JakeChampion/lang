//go:build !linux && !darwin

package interp

import "syscall"

func hostSync() {}

func hostFsync(int) error { return syscall.ENOSYS }

func hostFdatasync(int) error { return syscall.ENOSYS }

func hostSyncfs(int) error { return syscall.ENOSYS }

func hostDropCache(int, int64, int64) error { return syscall.ENOSYS }
