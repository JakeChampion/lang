package e2eharness

import (
	"net"
	"testing"
)

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
