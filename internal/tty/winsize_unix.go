//go:build linux || darwin

package tty

import (
	"syscall"
	"unsafe"
)

// winsize is the kernel's `struct winsize`, identical on Linux and Darwin:
// four u16s, of which the two cell counts are the ones the language exposes.
type winsize struct {
	rows, cols, xpixel, ypixel uint16
}

// WindowSize reports how many rows and columns the terminal on the other end
// of fd has, by the same ioctl the native backends emit. The error is the
// kernel's: ENOTTY when fd is not a terminal, which is the answer a caller
// falling back to COLUMNS is waiting for, and EBADF when it is not open.
func WindowSize(fd int) (rows, cols int, err error) {
	if fd < 0 {
		return 0, 0, syscall.EBADF
	}
	var ws winsize
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), tiocGWinSz, uintptr(unsafe.Pointer(&ws)), 0, 0, 0)
	if errno != 0 {
		return 0, 0, errno
	}
	return int(ws.rows), int(ws.cols), nil
}

// SetWindowSize tells the kernel how large the terminal on the other end of fd
// is, which is what a terminal emulator does on every resize. The write half of
// WindowSize, so a test can ask for a size it already knows the answer to.
func SetWindowSize(fd, rows, cols int) error {
	ws := winsize{rows: uint16(rows), cols: uint16(cols)}
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), tiocSWinSz, uintptr(unsafe.Pointer(&ws)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
