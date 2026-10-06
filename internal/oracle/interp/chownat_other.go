//go:build !unix

package interp

import "syscall"

// chownAt on a platform whose `syscall` package has no ownership call.
// ENOSYS rather than a silent success: a caller that believed a no-op
// had changed an owner would report a change that never happened.
func chownAt(string, int, int, bool) error { return syscall.ENOSYS }
