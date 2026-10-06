//go:build linux

package coreutils

import (
	"syscall"
	"unsafe"
)

// entryContext is the raw `security.selinux` value on path itself, a final
// symlink not followed, or "" when it carries none.
func entryContext(path string) string {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return ""
	}
	n, err := syscall.BytePtrFromString("security.selinux")
	if err != nil {
		return ""
	}
	buf := make([]byte, 4096)
	r, _, errno := syscall.Syscall6(syscall.SYS_LGETXATTR, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(n)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0)
	if errno != 0 {
		return ""
	}
	return string(buf[:r])
}

// setEntryContext labels path the way setfilecon(3) does, NUL included.
func setEntryContext(path, ctx string) error {
	return syscall.Setxattr(path, "security.selinux", append([]byte(ctx), 0), 0)
}
