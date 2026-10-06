//go:build linux

package tty

import (
	"syscall"
	"unsafe"
)

// The kernel's `struct termios` on the asm-generic ABI x86-64 and arm64 both
// use: four 32-bit flag words, one line-discipline byte, and NCCS = 19
// control characters. Thirty-six bytes, measured with a TCGETS into an
// over-long buffer — the bytes past 36 come back untouched.
//
// glibc's userspace struct is a DIFFERENT, wider one (32 control characters
// plus a derived input and output speed), which is why GNU `stty -g` prints
// 32 of them with the top 13 always zero. Nothing here goes through glibc, so
// what this reports is the kernel's 19 and the presentation padding belongs to
// the caller.
const (
	termiosNCCS  = 19
	termiosBytes = 4*4 + 1 + termiosNCCS
	// The four flags, the line byte, then the control characters: the
	// length Termios answers with and SetTermios requires.
	termiosWords = 4 + 1 + termiosNCCS
)

// Termios reads the line settings of the terminal on the other end of fd as
// the KERNEL's own words: iflag, oflag, cflag, lflag, the line discipline,
// then each control character. A caller prints these and reads them back
// (`stty -g`), so they are not normalised.
//
// The error is the kernel's, and ENOTTY is the one that matters: it is the
// whole of `stty: 'standard input': Inappropriate ioctl for device`.
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

// SetTermios writes the words Termios reports back to fd. `when` is Fern's
// own: 0 applies the change at once, 1 after the output drains, 2 after
// draining and discarding pending input — TCSETS, TCSETSW and TCSETSF, which
// are consecutive from TCSETS.
//
// A wrong-length array is EINVAL rather than a partial write: there is a
// fixed-size struct to fill and no way to guess the rest.
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
