//go:build darwin

package e2eharness

import (
	"bytes"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// OpenPTY is the Darwin half of the helper documented in pty_linux.go. XNU
// spells the same three steps differently: grant, unlock, then ask for the
// slave's path by name rather than by number.
func OpenPTY() (master, slave *os.File, err error) {
	const (
		tiocptygrant = 0x20007454
		tiocptyunlk  = 0x20007452
		tiocptygname = 0x40807453
	)
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	var name [128]byte
	for _, step := range []struct {
		req uintptr
		arg unsafe.Pointer
		who string
	}{
		{tiocptygrant, nil, "TIOCPTYGRANT"},
		{tiocptyunlk, nil, "TIOCPTYUNLK"},
		{tiocptygname, unsafe.Pointer(&name[0]), "TIOCPTYGNAME"},
	} {
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), step.req, uintptr(step.arg)); e != 0 {
			m.Close()
			return nil, nil, fmt.Errorf("%s: %v", step.who, e)
		}
	}
	path := string(name[:bytes.IndexByte(name[:], 0)])
	s, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, s, nil
}
