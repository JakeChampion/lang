//go:build !linux

package tty

import "syscall"

// Termios has no implementation off Linux yet. Darwin's `struct termios` is a
// different shape — unsigned-long flag words, twenty control characters, no
// line-discipline byte, and an input and output speed appended — and its
// ioctl numbers encode that struct's size, so neither can be written from
// memory and neither is measurable on the build box. ENOSYS rather than a
// guess: a wrong ioctl number answers ENOTTY, which a caller cannot tell
// from "this is not a terminal". This is the line `poll` is already on for
// the same target.
func Termios(int) ([]int64, error) { return nil, syscall.ENOSYS }

// SetTermios is unimplemented for the same reason.
func SetTermios(int, int, []int64) error { return syscall.ENOSYS }
