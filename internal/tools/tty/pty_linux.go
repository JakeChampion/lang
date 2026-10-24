//go:build linux

package tty

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func OpenPTY() (master, slave *os.File, err error) {
	const (
		tiocsptlck = 0x40045431
		tiocgptn   = 0x80045430
	)
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), tiocsptlck, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		m.Close()
		return nil, nil, fmt.Errorf("TIOCSPTLCK: %v", e)
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), tiocgptn, uintptr(unsafe.Pointer(&n))); e != 0 {
		m.Close()
		return nil, nil, fmt.Errorf("TIOCGPTN: %v", e)
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, s, nil
}
