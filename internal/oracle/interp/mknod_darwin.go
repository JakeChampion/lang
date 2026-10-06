//go:build darwin

package interp

import "syscall"

// mknodAt creates a FIFO or a device node with XNU's dev_t encoding,
// which is NOT Linux's: the major is one contiguous byte at the top and
// the minor is the 24 bits below it, where Linux splits the minor around
// the major.
//
//	dev[23:0]  = minor[23:0]
//	dev[31:24] = major[7:0]
//
// Unmeasured, unlike the Linux layout next door: there is no Darwin
// machine in this repository's dev loop, so this follows XNU's documented
// shape and is exercised only on the macos-15 CI runner. The case that
// does not depend on it is the one an unprivileged caller can actually
// reach: a FIFO passes dev 0, which is 0 under either encoding.
func mknodAt(path string, mode, major, minor uint32) error {
	dev := int(uint64(minor&0xffffff) | uint64(major&0xff)<<24)
	if major > 0xff || minor > 0xffffff {
		mode = badSIFMT
	}
	return syscall.Mknod(path, mode, dev)
}
