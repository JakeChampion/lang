//go:build linux

package interp

import "syscall"

const (
	atFdcwd   = -0x64
	atEaccess = 0x200
)

func accessEffective(path string, mode int) error {
	return syscall.Faccessat(atFdcwd, path, uint32(mode), atEaccess)
}
