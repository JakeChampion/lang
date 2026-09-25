package x86_64

import (
	"strings"
	"testing"
)

// udp_send has a gate of its own, so a datagram-only program links none of
// the TCP runtime.
func TestUdpSendLinksNoTcpRuntime(t *testing.T) {
	asm := compile(t, `function main(): i32 { return udp_send("127.0.0.1", 9, "x"); }`)
	helperBody(t, asm, "__fern_udp_send")
	if strings.Contains(asm, "__fern_tcp_") {
		t.Error("a udp_send-only program links the TCP runtime")
	}
}
