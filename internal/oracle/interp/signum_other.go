//go:build !linux && !darwin

package interp

const (
	sigKill = int64(9)
	sigStop = int64(19)
)
