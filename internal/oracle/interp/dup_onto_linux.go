//go:build linux

package interp

import "syscall"

func hostDupOnto(oldfd, newfd int) error { return syscall.Dup3(oldfd, newfd, 0) }
