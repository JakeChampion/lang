//go:build unix

package interp

import "syscall"

func setUmask(mask int) int {
	return syscall.Umask(mask)
}
