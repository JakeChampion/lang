//go:build !linux && !darwin

package tty

import "syscall"

func Termios(int) ([]int64, error) { return nil, syscall.ENOSYS }

func SetTermios(int, int, []int64) error { return syscall.ENOSYS }
