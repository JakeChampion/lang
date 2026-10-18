//go:build linux

package interp

import (
	"syscall"
	"unsafe"
)

// hostTimerFd is timerfd_create(CLOCK_MONOTONIC) armed once for ms, the two
// calls the native runtime makes; a failed create returns before the arm.
func hostTimerFd(ms int64) (int, error) {
	const clockMonotonic = 1
	fd, _, errno := syscall.Syscall(syscall.SYS_TIMERFD_CREATE, clockMonotonic, syscall.O_CLOEXEC, 0)
	if errno != 0 {
		return 0, errno
	}
	spec := [4]int64{0, 0, ms / 1000, (ms % 1000) * 1000000}
	if _, _, errno := syscall.Syscall6(syscall.SYS_TIMERFD_SETTIME, fd, 0, uintptr(unsafe.Pointer(&spec[0])), 0, 0, 0); errno != 0 {
		syscall.Close(int(fd))
		return 0, errno
	}
	return int(fd), nil
}
