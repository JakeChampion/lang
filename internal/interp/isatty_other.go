//go:build !linux && !darwin

package interp

// isattyFd has no portable answer off Linux/Darwin, and "not a
// terminal" is the safe one: it selects plain text.
func isattyFd(fd int) bool { return false }
