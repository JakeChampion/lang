//go:build darwin

package interp

import "syscall"

// hostDupOnto on XNU, which has no dup3(2).
func hostDupOnto(oldfd, newfd int) error { return syscall.Dup2(oldfd, newfd) }
