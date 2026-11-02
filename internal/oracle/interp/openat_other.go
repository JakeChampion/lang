//go:build !linux

package interp

import (
	"os"
	"syscall"
)

// openAtFile opens the joined path. The Go syscall package has openat on
// Linux only, so a symbolic link in the entry's place is refused by a check
// before the open.
func openAtFile(dir *os.File, path, name string, flag int) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, syscall.ELOOP
	}
	return os.OpenFile(path, flag, 0o666)
}
