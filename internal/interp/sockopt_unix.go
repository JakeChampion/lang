//go:build linux || darwin

package interp

import "syscall"

// The socket-option names and the local-address query the socket builtins
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
