//go:build linux

package interp

import "syscall"

// mknodAt creates a FIFO or a device node with Linux's dev_t encoding.
//
// The encoding was DERIVED BY MEASUREMENT — creating nodes with
// /usr/bin/mknod and reading the raw `st_rdev` back — rather than from
// glibc's `makedev`, which is a wider layout carrying a major past bit 31
// that this syscall rejects:
//
//	dev[7:0]   = minor[7:0]
//	dev[19:8]  = major[11:0]
//	dev[31:20] = minor[19:8]
//
// The minor is SPLIT around the major, and that is the whole trap: the
// legacy 8+8 layout agrees with this one for every pair that fits in a
// byte each, so (1, 3) and (255, 255) cannot tell the two apart while
// (1, 256) — 0x00100100 here, 0x0100 there — can. The kernel's own
// ceiling follows from the field widths and was measured at the
// boundary: major 4096 and minor 1048576 are both EINVAL.
func mknodAt(path string, mode, major, minor uint32) error {
	dev := int(uint64(minor&0xff) |
		uint64(major&0xfff)<<8 |
		uint64(minor&0xfff00)<<12)
	if major > 0xfff || minor > 0xfffff {
		mode = badSIFMT
	}
	// The mode goes to the kernel whole: the S_IFMT bits select the type
	// and the rest are the permission bits the umask then filters.
	return syscall.Mknod(path, mode, dev)
}
