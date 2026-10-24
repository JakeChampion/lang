//go:build !js && !plan9

package interp

import "syscall"

func nofileSoft() (uint64, error) {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return 0, err
	}
	return lim.Cur, nil
}
