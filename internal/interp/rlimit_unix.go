//go:build !js && !plan9

package interp

import "syscall"

// nofileSoft is the soft RLIMIT_NOFILE the kernel is enforcing on this
// process. RLIM_INFINITY is all ones on Linux and i64 max on Darwin, so the
// caller clamps rather than comparing against either spelling.
func nofileSoft() (uint64, error) {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return 0, err
	}
	return lim.Cur, nil
}
