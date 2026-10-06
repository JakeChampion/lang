//go:build !unix

package interp

// setUmask on a platform with no file-mode creation mask. Nothing masks
// a creation there, so the mask this reports is the one that describes
// that: zero, unchanged by any call.
func setUmask(int) int { return 0 }
