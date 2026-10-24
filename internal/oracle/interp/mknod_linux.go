//go:build linux

package interp

import "syscall"

func mknodAt(path string, mode, major, minor uint32) error {
	dev := int(uint64(minor&0xff) |
		uint64(major&0xfff)<<8 |
		uint64(minor&0xfff00)<<12)
	if major > 0xfff || minor > 0xfffff {
		mode = badSIFMT
	}
	return syscall.Mknod(path, mode, dev)
}
