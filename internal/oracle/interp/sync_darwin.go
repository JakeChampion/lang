//go:build darwin

package interp

import "syscall"

func hostSync() { syscall.Sync() }

func hostFsync(fd int) error { return syscall.Fsync(fd) }

func hostFdatasync(fd int) error { return syscall.Fsync(fd) }

func hostSyncfs(fd int) error {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return err
	}
	syscall.Sync()
	return nil
}

func hostDropCache(fd int, _, _ int64) error {
	if _, err := syscall.Seek(fd, 0, 1); err != nil {
		return err
	}
	return errNoFadvise
}
