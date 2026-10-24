//go:build linux || darwin

package tty

import (
	"syscall"
	"unsafe"
)

type winsize struct {
	rows, cols, xpixel, ypixel uint16
}

func WindowSize(fd int) (rows, cols int, err error) {
	rows, cols, _, _, err = WindowSizeFull(fd)
	return rows, cols, err
}

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
