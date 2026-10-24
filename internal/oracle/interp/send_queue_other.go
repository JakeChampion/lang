//go:build !linux && !darwin

package interp

import "syscall"

func hostSendQueued(fd int) (int, error) {
	return 0, syscall.ENOTSUP
}
