//go:build !linux && !darwin

package tty

import "syscall"

// Termios has no implementation off Linux and Darwin. ENOSYS rather than a
// guess: a wrong ioctl number answers ENOTTY, which a caller cannot tell from
// "this is not a terminal".
func Termios(int) ([]int64, error) { return nil, syscall.ENOSYS }

// SetTermios is unimplemented for the same reason.
func SetTermios(int, int, []int64) error { return syscall.ENOSYS }
