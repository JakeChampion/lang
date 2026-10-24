//go:build !darwin

package interp

import "syscall"

func sysctlMIB(mib []int32) ([]byte, error) {
	return nil, syscall.ENOSYS
}
