//go:build darwin

package interp

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

func openAtFile(dir *os.File, path, name string, flag int) (*os.File, error) {
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return nil, err
	}
	const sysOpenat = 463
	fd, _, errno := syscall.Syscall6(sysOpenat, dir.Fd(), uintptr(unsafe.Pointer(p)),
		uintptr(flag|syscall.O_CLOEXEC), 0o666, 0, 0)
	runtime.KeepAlive(dir)
	if errno != 0 {
		return nil, errno
	}
	return os.NewFile(fd, path), nil
}
