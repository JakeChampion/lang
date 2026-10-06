//go:build !linux && !darwin

package interp

import "syscall"

// A host with no descriptor table to rearrange must report that the
// operation is unavailable.
func hostDupOnto(int, int) error { return syscall.ENOSYS }
