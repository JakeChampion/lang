//go:build !linux && !darwin

package interp

import "syscall"

const (
	utimeOmit = 1<<30 - 2
	utimeNow  = 1<<30 - 1
)

func setFileTimes(path string, times *[2]syscall.Timespec, nofollow bool) error {
	if nofollow {
		return syscall.EOPNOTSUPP
	}
	return syscall.UtimesNano(path, times[:])
}
