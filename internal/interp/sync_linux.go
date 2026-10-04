//go:build linux

package interp

import "syscall"

func hostSync() { syscall.Sync() }

func hostFsync(fd int) error { return syscall.Fsync(fd) }

func hostFdatasync(fd int) error { return syscall.Fdatasync(fd) }

// hostSyncfs is `syncfs(2)`, which Go's syscall package declares no
// wrapper for, so the number is called directly. It does not come from
// syscall.SYS_SYNCFS either: that constant is in the arm64 table but not
// the amd64 one, whose zsysnum stops at 302. sysSyncfs carries it per
// GOARCH instead, and is 0 where the architecture has no such call.
func hostSyncfs(fd int) error {
	if sysSyncfs == 0 {
		return syscall.ENOSYS
	}
	if _, _, errno := syscall.Syscall(uintptr(sysSyncfs), uintptr(fd), 0, 0); errno != 0 {
		return errno
	}
	return nil
}

// posixFadvDontneed is POSIX_FADV_DONTNEED on Linux.
const posixFadvDontneed = 4

const hostHasDropCache = true

// hostDropCache is posix_fadvise(fd, off, n, POSIX_FADV_DONTNEED), which
// Go's syscall package has no wrapper for. It answers its errno directly
// rather than setting errno, so the raw call's result is the error.
func hostDropCache(fd int, off, n int64) error {
	if _, _, errno := syscall.Syscall6(syscall.SYS_FADVISE64, uintptr(fd), uintptr(off), uintptr(n), posixFadvDontneed, 0, 0); errno != 0 {
		return errno
	}
	return nil
}
