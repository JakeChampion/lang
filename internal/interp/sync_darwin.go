//go:build darwin

package interp

import "syscall"

func hostSync() { syscall.Sync() }

func hostFsync(fd int) error { return syscall.Fsync(fd) }

// hostFdatasync on XNU. The kernel does have fdatasync (syscall 187,
// which the arm64-darwin backend issues directly), but Go's `syscall`
// package exposes no wrapper for it and reaches Darwin through libc
// rather than raw syscall numbers, which Apple does not keep stable.
// fsync is fdatasync's strict superset — it flushes the data fdatasync
// would and the metadata fdatasync may skip — so the guarantee the
// caller asked for holds, at the cost of writing more than was needed.
func hostFdatasync(fd int) error { return syscall.Fsync(fd) }

// hostSyncfs on XNU, which has no syncfs(2) either. sync(2) flushes
// EVERY mounted filesystem, so it covers the one the descriptor lives on
// and the requested guarantee holds; the descriptor is fstat'ed first so
// that a closed or invalid one is still EBADF rather than being flushed
// past. Over-delivering the flush is not the same as reporting a flush
// that did not happen.
func hostSyncfs(fd int) error {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return err
	}
	syscall.Sync()
	return nil
}
