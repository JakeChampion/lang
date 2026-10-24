//go:build linux || darwin

package interp

import "syscall"

const (
	solSocket   = syscall.SOL_SOCKET
	soKeepalive = syscall.SO_KEEPALIVE
	tcpNodelay  = syscall.TCP_NODELAY
	ipprotoIPv6 = syscall.IPPROTO_IPV6
	ipv6V6only  = syscall.IPV6_V6ONLY
)

func getsockname(fd int) (syscall.Sockaddr, error) {
	return syscall.Getsockname(fd)
}

func getpeername(fd int) (syscall.Sockaddr, error) {
	return syscall.Getpeername(fd)
}

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
