//go:build linux || darwin

package interp

import "syscall"

// The socket-option names and the address queries the socket builtins
// use, which the js/wasm syscall package the playground is built against
// does not define (sockopt_other.go).
const (
	solSocket   = syscall.SOL_SOCKET
	soKeepalive = syscall.SO_KEEPALIVE
	tcpNodelay  = syscall.TCP_NODELAY
)

func getsockname(fd int) (syscall.Sockaddr, error) {
	return syscall.Getsockname(fd)
}

func getpeername(fd int) (syscall.Sockaddr, error) {
	return syscall.Getpeername(fd)
}

// connectResult is how a connect under way on fd ended: nil once a peer is
// attached, EINPROGRESS while none is and no error is pending, else the
// errno the connect failed with, read through SO_ERROR.
func connectResult(fd int) error {
	if _, err := syscall.Getpeername(fd); err == nil {
		return nil
	}
	pending, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_ERROR)
	switch {
	case err != nil:
		return err
	case pending != 0:
		return syscall.Errno(pending)
	}
	return syscall.EINPROGRESS
}
