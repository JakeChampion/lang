//go:build !linux && !darwin

package interp

import "syscall"

const (
	solSocket   = 0
	soKeepalive = 0
	tcpNodelay  = 0
	ipprotoIPv6 = 0
	ipv6V6only  = 0

	ipprotoIP      = 0
	ipprotoUDP     = 0
	ipTOS          = 0
	ipv6Tclass     = 0
	ipv6RecvTclass = 0
)

func getsockname(fd int) (syscall.Sockaddr, error) {
	return nil, syscall.ENOSYS
}

func getpeername(fd int) (syscall.Sockaddr, error) {
	return nil, syscall.ENOSYS
}

func connectResult(fd int) error { return syscall.ENOSYS }
