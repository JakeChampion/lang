//go:build js || plan9

package interp

import "syscall"

func nofileSoft() (uint64, error) { return 0, syscall.ENOSYS }
