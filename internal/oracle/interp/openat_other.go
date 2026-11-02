//go:build !linux && !darwin

package interp

import (
	"os"
	"syscall"
)

// Hosts without a native directory-relative open use the joined path.
// The link check precedes the open on these unsupported native targets.
func openAtFile(dir *os.File, path, name string, flag int) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, syscall.ELOOP
	}
	return os.OpenFile(path, flag, 0o666)
}
