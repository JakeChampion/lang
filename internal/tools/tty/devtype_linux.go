//go:build linux

package tty

func deviceTypeIsTTY(int) (isTTY, ok bool) { return false, false }
