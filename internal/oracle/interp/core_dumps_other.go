//go:build js || plan9

package interp

func disableCoreDumps() bool { return false }
