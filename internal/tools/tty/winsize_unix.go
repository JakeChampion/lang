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
	rows, cols, _, _, err = WindowSizeFull(fd)
	return rows, cols, err
}

// WindowSizeFull reports all four fields of the kernel's record, including the
// pixel pair the language does not expose. `set_window_size` PRESERVES that
// pair, as GNU's stty does, and this is what proves it.
func WindowSizeFull(fd int) (rows, cols, xpixel, ypixel int, err error) {
	if fd < 0 {
		return 0, 0, 0, 0, syscall.EBADF
	}
	var ws winsize
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), tiocGWinSz, uintptr(unsafe.Pointer(&ws)), 0, 0, 0)
	if errno != 0 {
		return 0, 0, 0, 0, errno
	}
	return int(ws.rows), int(ws.cols), int(ws.xpixel), int(ws.ypixel), nil
}

// SetWindowSizeFull writes all four fields, which is the only way to put a
// pixel pair on a descriptor: nothing else here surrenders one.
func SetWindowSizeFull(fd, rows, cols, xpixel, ypixel int) error {
	if fd < 0 {
		return syscall.EBADF
	}
	ws := winsize{rows: uint16(rows), cols: uint16(cols), xpixel: uint16(xpixel), ypixel: uint16(ypixel)}
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), tiocSWinSz, uintptr(unsafe.Pointer(&ws)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// SetWindowSize tells the kernel how large the terminal on the other end of fd
// is, which is what a terminal emulator does on every resize. The write half of
// WindowSize, so a test can ask for a size it already knows the answer to, and
// the interpreter's `set_window_size(fd, rows, cols)`.
//
// Read-modify-write, because the pixel pair beside the cell counts is kept by
// whoever wrote it last and neither WindowSize nor the builtin surrenders it:
// a caller given only rows and columns cannot put back what it never saw.
// Both numbers reach the kernel's u16 as given, so 65536 rows lands as 0.
func SetWindowSize(fd, rows, cols int) error {
	if fd < 0 {
		return syscall.EBADF
	}
	var ws winsize
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), tiocGWinSz, uintptr(unsafe.Pointer(&ws)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return SetWindowSizeFull(fd, rows, cols, int(ws.xpixel), int(ws.ypixel))
}
