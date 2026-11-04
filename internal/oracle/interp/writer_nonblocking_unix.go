//go:build unix

package interp

import (
	"os"
	"syscall"
)

// On a pipe from os.Pipe, each File.Fd call clears O_NONBLOCK, and
// write_some calls File.Fd. A file that os.NewFile makes after the flag is
// set keeps the flag.
func hostReopenNonblock(f *os.File, on bool) (*os.File, error) {
	if !on {
		return f, hostSetNonblock(f, false)
	}
	nfd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		return f, err
	}
	syscall.CloseOnExec(nfd)
	if err := syscall.SetNonblock(nfd, true); err != nil {
		syscall.Close(nfd)
		return f, err
	}
	nf := os.NewFile(uintptr(nfd), f.Name())
	f.Close()
	return nf, nil
}

func hostSetNonblock(f *os.File, on bool) error {
	return syscall.SetNonblock(int(f.Fd()), on)
}
