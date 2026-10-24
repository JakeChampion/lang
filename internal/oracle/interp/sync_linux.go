//go:build linux

package interp

import "syscall"

func hostSync() { syscall.Sync() }

func hostFsync(fd int) error { return syscall.Fsync(fd) }

func hostFdatasync(fd int) error { return syscall.Fdatasync(fd) }

func hostSyncfs(fd int) error {
	if sysSyncfs == 0 {
		return syscall.ENOSYS
	}
	if _, _, errno := syscall.Syscall(uintptr(sysSyncfs), uintptr(fd), 0, 0); errno != 0 {
		return errno
	}
	return nil
}

const posixFadvDontneed = 4

func hostDropCache(fd int, off, n int64) error {
	if _, _, errno := syscall.Syscall6(syscall.SYS_FADVISE64, uintptr(fd), uintptr(off), uintptr(n), posixFadvDontneed, 0, 0); errno != 0 {
		return errno
	}
	return nil
}
