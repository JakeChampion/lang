//go:build linux

package interp

import "syscall"

// hostDupOnto is dup3(2) with no flags. Linux/arm64 has no dup2 at all,
// so dup3 is the form both Linux architectures carry.
func hostDupOnto(oldfd, newfd int) error { return syscall.Dup3(oldfd, newfd, 0) }
