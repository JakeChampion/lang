//go:build linux

package interp

import (
	"os"
	"syscall"
)

// openAtFile opens name relative to dir through openat, so the entry resolves
// under the directory the handle holds open, wherever that directory now is.
func openAtFile(dir *os.File, path, name string, flag int) (*os.File, error) {
	fd, err := syscall.Openat(int(dir.Fd()), name, flag|syscall.O_CLOEXEC, 0o666)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
