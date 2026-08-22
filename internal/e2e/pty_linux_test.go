//go:build linux

package e2e

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// openPTY returns a freshly allocated pseudo-terminal pair. The slave is
// what a child process gets as its stdout when a test needs `isatty(1)` to
// be true; the master is what the test reads that output back from.
//
// Hand-rolled because the module has no third-party dependencies and the
// standard library has no pty. Linux: open /dev/ptmx, unlock the pair
// (TIOCSPTLCK with 0), ask for its number (TIOCGPTN), open /dev/pts/N.
func openPTY() (master, slave *os.File, err error) {
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
