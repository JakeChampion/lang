//go:build !darwin

package interp

import "syscall"

// sysctlMIB answers ENOSYS: Linux removed sysctl(2), and the platforms gate
// grants the builtin on Darwin only.
func sysctlMIB(mib []int32) ([]byte, error) {
	return nil, syscall.ENOSYS
}
