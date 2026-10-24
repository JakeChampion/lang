//go:build !linux && !darwin

package tty

func IsTerminal(fd int) bool { return false }
