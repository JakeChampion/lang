//go:build !linux && !darwin

package interp

import "syscall"

// A host without raw sockets: the option names are inert and the
// local-address query answers ENOSYS, like every socket call there.
const (
	solSocket   = 0
	soKeepalive = 0
	tcpNodelay  = 0
)

func getsockname(fd int) (syscall.Sockaddr, error) {
	return nil, syscall.ENOSYS
}
