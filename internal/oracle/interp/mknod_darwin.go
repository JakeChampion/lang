//go:build darwin

package interp

import "syscall"

func mknodAt(path string, mode, major, minor uint32) error {
	dev := int(uint64(minor&0xffffff) | uint64(major&0xff)<<24)
	if major > 0xff || minor > 0xffffff {
		mode = badSIFMT
	}
	return syscall.Mknod(path, mode, dev)
}
