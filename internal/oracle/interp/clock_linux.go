//go:build linux

package interp

import (
	"syscall"
	"unsafe"
)

const clockRealtime = 0

func hostClockResolution() int64 {
	var ts syscall.Timespec
	syscall.Syscall(syscall.SYS_CLOCK_GETRES, clockRealtime, uintptr(unsafe.Pointer(&ts)), 0)
	return ts.Sec*1_000_000_000 + ts.Nsec
}

func hostClockSet(sec, nsec int64) error {
	ts := syscall.Timespec{Sec: sec, Nsec: nsec}
	if _, _, errno := syscall.Syscall(syscall.SYS_CLOCK_SETTIME, clockRealtime, uintptr(unsafe.Pointer(&ts)), 0); errno != 0 {
		return errno
	}
	return nil
}
