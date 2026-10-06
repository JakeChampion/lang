//go:build !linux && !darwin

package interp

import (
	"syscall"
	"time"
)

// hostClockResolution on a host with no clock_getres Fern reaches measures
// the clock instead, the way gnulib's gettime_res does without one: the
// greatest common divisor of the subsecond parts of a few readings.
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

// hostClockSet on the same host: there is no clock Fern can set there, and
// reporting success would claim a change that did not happen.
func hostClockSet(int64, int64) error { return syscall.ENOSYS }
