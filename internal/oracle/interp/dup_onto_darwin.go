//go:build darwin

package interp

import "syscall"

func hostDupOnto(oldfd, newfd int) error { return syscall.Dup2(oldfd, newfd) }
