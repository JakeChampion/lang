//go:build darwin

package interp

import "syscall"

// soNWrite is SO_NWRITE: the bytes in a socket's send buffer.
const soNWrite = 0x1024

// hostSendQueued is the bytes queued to send on a socket, unsent and
// unacknowledged alike.
func hostSendQueued(fd int) (int, error) {
	return syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, soNWrite)
}
