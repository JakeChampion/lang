//go:build darwin

package interp

// Darwin has no UDP_GRO, so socket op 10 answers -ENOPROTOOPT. Its
// IP_RECVTOS is 27, which Go's syscall package does not name.
const (
	udpGRO    = -1
	ipRecvTOS = 27
)
