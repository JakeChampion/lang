//go:build !linux && !darwin

package interp

import "syscall"

// mknodAt on a platform whose `syscall` package has neither the call nor
// a dev_t layout this package knows. ENOSYS rather than a silent success
// or a node of some other type: the caller gets an `Err` naming the
// absence, the same answer fsstat_other.go gives for a `statfs` it cannot
// project.
func mknodAt(string, uint32, uint32, uint32) error { return syscall.ENOSYS }
