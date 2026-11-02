//go:build unix

package interp

import "syscall"

const (
	oNonblock  = syscall.O_NONBLOCK
	oDirectory = syscall.O_DIRECTORY
	oDsync     = syscall.O_DSYNC
	oSync      = syscall.O_SYNC
	oNoctty    = syscall.O_NOCTTY
	oNofollow  = syscall.O_NOFOLLOW
)
