//go:build darwin

package interp

import "syscall"

const soNWrite = 0x1024

func hostSendQueued(fd int) (int, error) {
	return syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, soNWrite)
}
