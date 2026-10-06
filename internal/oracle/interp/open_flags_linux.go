//go:build linux

package interp

import "syscall"

// O_DIRECT and O_NOATIME are Linux's alone: XNU spells neither (its
// F_NOCACHE is a request made after the open), so on every other host
// the two bits are refused.
const (
	oDirect  = syscall.O_DIRECT
	oNoatime = syscall.O_NOATIME
)
