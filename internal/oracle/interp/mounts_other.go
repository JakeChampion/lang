//go:build !linux && !darwin

package interp

import "syscall"

const mountTablePath = ""

func mountRows() ([]rawMount, error) { return nil, syscall.ENOSYS }
