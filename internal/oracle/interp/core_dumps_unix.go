//go:build !linux && !js && !plan9

package interp

import "syscall"

func disableCoreDumps() bool {
	return syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{}) == nil
}
