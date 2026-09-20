//go:build !linux && !darwin

package interp

import "syscall"

// chmodAt on a platform whose nofollow form this package does not know.
// ENOSYS rather than a silent success: a caller that believed a no-op
// had changed a mode would report a change that never happened.
func chmodAt(string, uint32, bool) error { return syscall.ENOSYS }
