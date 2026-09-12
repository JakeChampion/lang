//go:build !linux && !darwin

package tty

import "syscall"

// WindowSize has no portable answer off Linux/Darwin. ENOTTY rather than a
// guessed 80x24: the caller's fallback is what should decide, not this file.
func WindowSize(int) (int, int, error) { return 0, 0, syscall.ENOTTY }

// SetWindowSize has no portable answer off Linux/Darwin either.
func SetWindowSize(int, int, int) error { return syscall.ENOTTY }
