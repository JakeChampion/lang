//go:build !unix

package interp

func setUmask(int) int { return 0 }
