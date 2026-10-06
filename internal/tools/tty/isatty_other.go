//go:build !linux && !darwin

package tty

// IsTerminal has no portable answer off Linux/Darwin, and "not a terminal" is
// the safe one: it selects plain text.
func IsTerminal(fd int) bool { return false }
