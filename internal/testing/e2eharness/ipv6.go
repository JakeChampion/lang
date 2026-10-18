package e2eharness

import "net"

// HostHasIPv6 reports whether this host can bind the IPv6 loopback.
func HostHasIPv6() bool {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		return false
	}
	ln.Close()
	return true
}
