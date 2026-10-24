//go:build !linux && !darwin

package interp

import (
	"syscall"
	"time"
)

func hostClockResolution() int64 {
	r := int64(1_000_000_000)
	for i := 0; i < 32 && r > 1; i++ {
		if sub := time.Now().UnixNano() % 1_000_000_000; sub > 0 {
			for sub != 0 {
				r, sub = sub, r%sub
			}
		}
	}
	return r
}

func hostClockSet(int64, int64) error { return syscall.ENOSYS }
