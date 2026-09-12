//go:build unix

package interp

import "syscall"

// chownAt sets the owner and the group of `path`.
//
// Split over `follow` rather than over the platform: `syscall.Chown` and
// `syscall.Lchown` are exactly `fchownat`'s two follow modes and both
// exist on every unix Go builds for, so there is no per-kernel encoding
// here the way mknod_linux.go / mknod_darwin.go have one.
//
// -1 for either id leaves that half alone. Both are passed through
// unexamined: the sentinel is the kernel's own, not something this layer
// substitutes for.
func chownAt(path string, uid, gid int, follow bool) error {
	if follow {
		return syscall.Chown(path, uid, gid)
	}
	return syscall.Lchown(path, uid, gid)
}
