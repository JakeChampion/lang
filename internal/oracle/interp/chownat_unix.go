//go:build unix

package interp

import "syscall"

func chownAt(path string, uid, gid int, follow bool) error {
	if follow {
		return syscall.Chown(path, uid, gid)
	}
	return syscall.Lchown(path, uid, gid)
}
