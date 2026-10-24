//go:build darwin

package tty

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

const (
	termiosNCCS  = 20
	termiosBytes = 72
	termiosWords = 4 + termiosNCCS + 2
	speedOffset  = 56
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
		out[i] = int64(binary.LittleEndian.Uint64(buf[i*8:]))
	}
	for i := 0; i < termiosNCCS; i++ {
		out[4+i] = int64(buf[32+i])
	}
	out[4+termiosNCCS] = int64(binary.LittleEndian.Uint64(buf[speedOffset:]))
	out[5+termiosNCCS] = int64(binary.LittleEndian.Uint64(buf[speedOffset+8:]))
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
		binary.LittleEndian.PutUint64(buf[i*8:], uint64(words[i]))
	}
	for i := 0; i < termiosNCCS; i++ {
		buf[32+i] = byte(words[4+i])
	}
	binary.LittleEndian.PutUint64(buf[speedOffset:], uint64(words[4+termiosNCCS]))
	binary.LittleEndian.PutUint64(buf[speedOffset+8:], uint64(words[5+termiosNCCS]))
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), tcSetAttr+uintptr(when),
		uintptr(unsafe.Pointer(&buf[0])), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

const tiocExt = 0x80047460

func SetExtproc(fd int, on bool) error {
	if fd < 0 {
		return syscall.EBADF
	}
	var v int32
	if on {
		v = 1
	}
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), tiocExt,
		uintptr(unsafe.Pointer(&v)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
