//go:build !linux && !darwin

package interp

import "syscall"

const mountTablePath = ""

// ENOSYS rather than an empty table: no mounts would be a measurement nobody
// took.
func mountRows() ([]rawMount, error) { return nil, syscall.ENOSYS }
