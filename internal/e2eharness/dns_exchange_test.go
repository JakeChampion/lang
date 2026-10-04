package e2eharness

import (
	"net"
	"testing"
)

// A port whose TCP side is already taken is given back, and the pair lands on
// one port both protocols hold. TestSelfHostDnsNat64 failed on the bind this
// skips, when another test's connection held the TCP side of the free UDP port.
func TestListenLoopbackPairSkipsATakenTCPPort(t *testing.T) {
	held, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	taken := held.Addr().(*net.TCPAddr).Port
	udp, tcp, err := listenLoopbackPair(taken)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	defer tcp.Close()
	up, tp := udp.LocalAddr().(*net.UDPAddr).Port, tcp.Addr().(*net.TCPAddr).Port
	if up == taken || up != tp {
		t.Errorf("udp on %d and tcp on %d, want one port that is not the taken %d", up, tp, taken)
	}
}
