//go:build !linux

package interp

import "syscall"

// utimeOmit is UTIME_OMIT. `syscall.UtimesNano` hands the timespec array
// to utimensat(2) verbatim on every platform that has one, so the
// sentinel reaches the kernel unchanged here too.
const utimeOmit = 1<<30 - 2

// setFileTimes is utimensat(AT_FDCWD, path, times, flags) on a platform
// whose `syscall` package exports neither the call nor its number.
//
// `syscall.UtimesNano` reaches the same syscall with a zero flags word,
// so everything but AT_SYMLINK_NOFOLLOW is exact — both omit sentinels
// included, since the timespecs pass through untouched.
//
// Nofollow is what is missing, and it is refused rather than quietly
// downgraded to a follow: setting the times of the symlink and setting
// the times of what it points at are different writes to different
// inodes, and a caller that asked for the first would otherwise be told
// it got it. EOPNOTSUPP is what the kernel answers for a flag it will
// not honour, and it reaches the caller as the `IoError` like any other
// errno. The compiled backends issue the syscall themselves and have no
// such gap.
func setFileTimes(path string, times *[2]syscall.Timespec, nofollow bool) error {
	if nofollow {
		return syscall.EOPNOTSUPP
	}
	return syscall.UtimesNano(path, times[:])
}
