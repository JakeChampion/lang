//go:build !linux && !darwin

package interp

// No UDP_GRO and no TOS option here: socket op 10 answers -ENOPROTOOPT,
// and the socket layer itself answers -ENOSYS.
const (
	udpGRO    = -1
	ipRecvTOS = 0
)
