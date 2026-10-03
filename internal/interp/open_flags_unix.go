//go:build unix

package interp

import "syscall"

// The kernel words the open_*_with builtins' flags word becomes, in the
// host's spelling. A zero means the host has no such flag and the bit is
// refused (openWithHelper).
const (
	oNonblock  = syscall.O_NONBLOCK
	oDirectory = syscall.O_DIRECTORY
	oDsync     = syscall.O_DSYNC
	oSync      = syscall.O_SYNC
	oNoctty    = syscall.O_NOCTTY
	oNofollow  = syscall.O_NOFOLLOW
)
