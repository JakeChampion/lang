//go:build darwin

package interp

import (
	"syscall"
	"unsafe"
)

// xattrBufBytes bounds a value read in one call; a longer one is ERANGE.
const xattrBufBytes = 65536

// getxattrBytes is getxattr(2), BSD 234, whose sixth argument is the
// options word: XATTR_NOFOLLOW (1) asks about a final symlink itself.
func getxattrBytes(path, name string, follow bool) ([]byte, error) {
	const sysGetxattr = 234
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return nil, err
	}
	n, err := syscall.BytePtrFromString(name)
	if err != nil {
		return nil, err
	}
	var options uintptr
	if !follow {
		options = 1
	}
	buf := make([]byte, xattrBufBytes)
	r, _, errno := syscall.Syscall6(sysGetxattr, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(n)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, options)
	if errno != 0 {
		return nil, errno
	}
	return buf[:r], nil
}

// setxattrBytes is setxattr(2), BSD 236: (path, name, value, size,
// position, options), with XATTR_NOFOLLOW for the `l` form.
func setxattrBytes(path, name string, value []byte, follow bool) error {
	const sysSetxattr = 236
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	n, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	var options uintptr
	if !follow {
		options = 1
	}
	var v unsafe.Pointer
	if len(value) > 0 {
		v = unsafe.Pointer(&value[0])
	}
	_, _, errno := syscall.Syscall6(sysSetxattr, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(n)),
		uintptr(v), uintptr(len(value)), 0, options)
	if errno != 0 {
		return errno
	}
	return nil
}
