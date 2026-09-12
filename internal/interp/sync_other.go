//go:build !linux && !darwin

package interp

import "syscall"

// Write-back on a platform whose `syscall` package has none of these
// calls. The three that answer a Result say ENOSYS, the way
// mknod_other.go does: a named absence rather than a success nobody
// got.
//
// `sync` itself returns void and has no way to report anything, so
// there is nothing to say here. Reaching it needs the `fssync` target
// capability, which only the hosted natives hold, so a compiled program
// on such a host is refused at E066 and never arrives.
func hostSync() {}

func hostFsync(int) error { return syscall.ENOSYS }

func hostFdatasync(int) error { return syscall.ENOSYS }

func hostSyncfs(int) error { return syscall.ENOSYS }
