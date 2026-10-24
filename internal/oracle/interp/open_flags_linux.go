//go:build linux

package interp

import "syscall"

const (
	oDirect  = syscall.O_DIRECT
	oNoatime = syscall.O_NOATIME
)
