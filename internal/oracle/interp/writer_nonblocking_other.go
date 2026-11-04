//go:build !unix

package interp

import (
	"os"
	"syscall"
)

func hostReopenNonblock(f *os.File, on bool) (*os.File, error) { return f, syscall.ENOSYS }

func hostSetNonblock(*os.File, bool) error { return syscall.ENOSYS }
