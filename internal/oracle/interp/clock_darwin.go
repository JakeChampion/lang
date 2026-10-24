//go:build darwin

package interp

import "syscall"

func hostClockResolution() int64 { return 1000 }

func hostClockSet(sec, nsec int64) error {
	if nsec < 0 || nsec >= 1_000_000_000 {
		return syscall.EINVAL
	}
	tv := syscall.Timeval{Sec: sec, Usec: int32(nsec / 1000)}
	return syscall.Settimeofday(&tv)
}
