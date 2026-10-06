//go:build linux

package interp

import (
	"syscall"
	"unsafe"
)

// clockRealtime is CLOCK_REALTIME, the clock now_ns reads.
const clockRealtime = 0

// hostClockResolution is clock_getres(CLOCK_REALTIME) in nanoseconds. Go's
// syscall package declares no wrapper, so the number is called directly; with
// a fixed clock id and a buffer of our own it cannot fail.
func hostClockResolution() int64 {
	var ts syscall.Timespec
	syscall.Syscall(syscall.SYS_CLOCK_GETRES, clockRealtime, uintptr(unsafe.Pointer(&ts)), 0)
	return ts.Sec*1_000_000_000 + ts.Nsec
}

// hostClockSet is clock_settime(CLOCK_REALTIME). The kernel validates the
// nanoseconds before it checks the privilege, so a bad value is EINVAL at any
// privilege.
func hostClockSet(sec, nsec int64) error {
	ts := syscall.Timespec{Sec: sec, Nsec: nsec}
	if _, _, errno := syscall.Syscall(syscall.SYS_CLOCK_SETTIME, clockRealtime, uintptr(unsafe.Pointer(&ts)), 0); errno != 0 {
		return errno
	}
	return nil
}
