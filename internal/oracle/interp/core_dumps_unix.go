//go:build !linux && !js && !plan9

package interp

import "syscall"

// disableCoreDumps zeroes RLIMIT_CORE, the only switch Darwin has.
func disableCoreDumps() bool {
	return syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{}) == nil
}
