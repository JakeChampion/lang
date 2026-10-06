//go:build darwin

package interp

import "syscall"

// hostClockResolution is the tick of the clock now_ns reads on Darwin.
// XNU has no clock_getres syscall; the wall clock is gettimeofday's, whose
// subsecond field counts microseconds, and libSystem's clock_getres answers
// the same 1000 ns for CLOCK_REALTIME.
func hostClockResolution() int64 { return 1000 }

// hostClockSet is settimeofday(2), XNU's only way to set the clock, so the
// nanoseconds are truncated to its microseconds. XNU checks the privilege
// before the value, so an out-of-range nsec is refused here first to answer
// EINVAL as Linux does.
func hostClockSet(sec, nsec int64) error {
	if nsec < 0 || nsec >= 1_000_000_000 {
		return syscall.EINVAL
	}
	tv := syscall.Timeval{Sec: sec, Usec: int32(nsec / 1000)}
	return syscall.Settimeofday(&tv)
}
