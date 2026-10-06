//go:build !linux && !darwin

package interp

import "syscall"

// hostSendQueued has no host reading of the send queue here.
func hostSendQueued(fd int) (int, error) {
	return 0, syscall.ENOTSUP
}
