//go:build !unix

package interp

import (
	"os"
	"syscall"
)

const (
	oNonblock  = 0
	oDirectory = 0
	oDsync     = 0
	oSync      = 0
	oNoctty    = 0
	oNofollow  = 0
)

// openAtFile opens the joined path. Without openat, a symbolic link in the
// entry's place is refused by a check before the open.
func openAtFile(dir *os.File, path, name string, flag int) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, syscall.ELOOP
	}
	return os.OpenFile(path, flag, 0o666)
}
