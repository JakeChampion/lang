//go:build !linux && !darwin

package interp

import "syscall"

// utimeOmit and utimeNow are UTIME_OMIT and UTIME_NOW.
const (
	utimeOmit = 1<<30 - 2
	utimeNow  = 1<<30 - 1
)

// setFileTimes delegates to the platform's syscall.UtimesNano fallback.
// It cannot express nofollow, so reject that request explicitly.
func setFileTimes(path string, times *[2]syscall.Timespec, nofollow bool) error {
	if nofollow {
		return syscall.EOPNOTSUPP
	}
	return syscall.UtimesNano(path, times[:])
}
