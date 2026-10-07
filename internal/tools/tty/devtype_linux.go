//go:build linux

package tty

// deviceTypeIsTTY has nothing to ask on Linux, whose isatty(3) is TCGETS
// alone.
func deviceTypeIsTTY(int) (isTTY, ok bool) { return false, false }
