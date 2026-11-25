//go:build linux

package e2eharness

import (
	"encoding/binary"
	"runtime"
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
	// A VSOCK connect has a timeout, so a signal ends it with EINTR. The
	// program under test has no signal handlers, and the test process gets
	// SIGURG from the Go runtime and SIGCHLD from its children.
	withSignalsBlocked(func() {
		_, _, e = syscall.Syscall(syscall.SYS_CONNECT, fd, uintptr(unsafe.Pointer(&sa[0])), uintptr(len(sa)))
	})
	return int(e), true
}

// withSignalsBlocked runs f on one OS thread with each signal blocked. A
// signal that comes during f stays pending until f returns.
func withSignalsBlocked(f func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	all := ^uint64(0)
	var old uint64
	const sigBlock, sigSetmask = 0, 2
	syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, sigBlock, uintptr(unsafe.Pointer(&all)), uintptr(unsafe.Pointer(&old)), 8, 0, 0)
	defer syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, sigSetmask, uintptr(unsafe.Pointer(&old)), 0, 8, 0, 0)
	f()
}
