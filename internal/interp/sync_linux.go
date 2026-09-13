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
