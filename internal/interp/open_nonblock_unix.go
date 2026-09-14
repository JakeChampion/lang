//go:build unix

package interp

import "syscall"

// oNonblock is the O_NONBLOCK the open_*_with builtins' bit 1 becomes.
const oNonblock = syscall.O_NONBLOCK
