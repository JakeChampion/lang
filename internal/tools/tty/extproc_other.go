//go:build !darwin

package tty

import "syscall"

// SetExtproc has no ioctl off Darwin: Linux sets EXTPROC through the
// termios words, and answers ENOTTY to a request it does not know.
func SetExtproc(fd int, _ bool) error {
	if fd < 0 {
		return syscall.EBADF
	}
	return syscall.ENOTTY
}
