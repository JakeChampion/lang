//go:build linux

package interp

import "syscall"

func hostSync() { syscall.Sync() }

func hostFsync(fd int) error { return syscall.Fsync(fd) }

func hostFdatasync(fd int) error { return syscall.Fdatasync(fd) }

// hostSyncfs is `syncfs(2)`, which Go's syscall package declares no
// wrapper for, so the number is called directly.
func hostSyncfs(fd int) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_SYNCFS, uintptr(fd), 0, 0); errno != 0 {
		return errno
	}
	return nil
}
