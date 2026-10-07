//go:build darwin

package interp

import (
	"os"
	"syscall"
	"unsafe"
)

// fclonefileat(2), and the flags __fern_clone_file passes it.
const (
	sysFclonefileat  = 517
	cloneNofollow    = 0x1
	cloneNoownercopy = 0x2
	atFdcwdDarwin    = -2
)

// cloneFile is fclonefileat from an open src, so a symlinked src is
// followed and a symlinked dest is not. dest must not exist.
func cloneFile(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err.(*os.PathError).Err
	}
	defer f.Close()
	d, err := syscall.BytePtrFromString(dest)
	if err != nil {
		return err
	}
	fdcwd := atFdcwdDarwin
	if _, _, e := syscall.Syscall6(sysFclonefileat, f.Fd(), uintptr(fdcwd), uintptr(unsafe.Pointer(d)), cloneNofollow|cloneNoownercopy, 0, 0); e != 0 {
		return e
	}
	return nil
}
