//go:build linux

package tty

import (
	"syscall"
	"unsafe"
)

const (
	termiosNCCS  = 19
	termiosBytes = 4*4 + 1 + termiosNCCS
	termiosWords = 4 + 1 + termiosNCCS
)

func Termios(fd int) ([]int64, error) {
	if fd < 0 {
		return nil, syscall.EBADF
	}
	var buf [termiosBytes]byte
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), tcGetAttr,
		uintptr(unsafe.Pointer(&buf[0])), 0, 0, 0)
	if errno != 0 {
		return nil, errno
	}
	out := make([]int64, termiosWords)
	for i := 0; i < 4; i++ {
		out[i] = int64(uint32(buf[i*4]) | uint32(buf[i*4+1])<<8 |
			uint32(buf[i*4+2])<<16 | uint32(buf[i*4+3])<<24)
	}
	out[4] = int64(buf[16])
	for i := 0; i < termiosNCCS; i++ {
		out[5+i] = int64(buf[17+i])
	}
	return out, nil
}

func SetTermios(fd, when int, words []int64) error {
	if fd < 0 {
		return syscall.EBADF
	}
	if len(words) != termiosWords || when < 0 || when > 2 {
		return syscall.EINVAL
	}
	var buf [termiosBytes]byte
	for i := 0; i < 4; i++ {
		v := uint32(words[i])
		buf[i*4] = byte(v)
		buf[i*4+1] = byte(v >> 8)
		buf[i*4+2] = byte(v >> 16)
		buf[i*4+3] = byte(v >> 24)
	}
	buf[16] = byte(words[4])
	for i := 0; i < termiosNCCS; i++ {
		buf[17+i] = byte(words[5+i])
	}
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), tcSetAttr+uintptr(when),
		uintptr(unsafe.Pointer(&buf[0])), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
