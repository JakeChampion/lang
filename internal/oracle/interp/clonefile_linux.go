//go:build linux

package interp

import (
	"errors"
	"os"
	"syscall"
)

// FICLONE.
const ficlone = 0x40049409

// cloneFile opens dest exclusively, or as it stands when it exists, and
// asks FICLONE. A dest this call created is removed again when the clone
// fails.
func cloneFile(src, dest string) error {
	s, err := os.Open(src)
	if err != nil {
		return err.(*os.PathError).Err
	}
	defer s.Close()
	made := true
	d, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if errors.Is(err, os.ErrExist) {
		made = false
		d, err = os.OpenFile(dest, os.O_WRONLY, 0)
	}
	if err != nil {
		return err.(*os.PathError).Err
	}
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, d.Fd(), ficlone, s.Fd())
	d.Close()
	if e != 0 {
		if made {
			os.Remove(dest)
		}
		return e
	}
	return nil
}
