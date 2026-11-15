//go:build linux

package e2eharness

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

// VsockConnectErrno connects a VSOCK stream socket to cid and port as the
// runtime does, and gives the errno of the socket or the connect, or 0. The
// second result is false off Linux.
func VsockConnectErrno(cid, port uint32) (int, bool) {
	fd, _, e := syscall.Syscall(syscall.SYS_SOCKET, 40, syscall.SOCK_STREAM, 0)
	if e != 0 {
		return int(e), true
	}
	defer syscall.Close(int(fd))
	var sa [16]byte
	binary.LittleEndian.PutUint16(sa[0:], 40)
	binary.LittleEndian.PutUint32(sa[4:], port)
	binary.LittleEndian.PutUint32(sa[8:], cid)
	_, _, e = syscall.Syscall(syscall.SYS_CONNECT, fd, uintptr(unsafe.Pointer(&sa[0])), uintptr(len(sa)))
	return int(e), true
}
