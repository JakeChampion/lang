//go:build linux

package tty

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// OpenPTY returns a freshly allocated pseudo-terminal pair. The slave is what
// a child process gets as its stdout when a test needs `isatty(1)` to be true;
// the master is what the test reads that output back from. Callers close both.
//
// Hand-rolled because the module has no third-party dependencies and the
// standard library has no pty. It lives in a non-test file on purpose: the
// build constraint belongs on a helper, not on a `*_test.go` name, where the
// go tool's implicit constraint would silently drop the tests along with it
// (internal/sourcelint's TestNoPlatformSuffixOnTestFiles, #5464).
//
// Linux: open /dev/ptmx, unlock the pair (TIOCSPTLCK with 0), ask for its
// number (TIOCGPTN), open /dev/pts/N.
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
