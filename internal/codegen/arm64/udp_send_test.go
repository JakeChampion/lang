package arm64

import (
	"strings"
	"testing"
)

const udpOnlySrc = `function main(): i32 { return udp_send("127.0.0.1", 9, "x"); }`

// udpSendBody is __fern_udp_send's text, up to the next global symbol: Mach-O
// output carries no `.size` to end it.
func udpSendBody(t *testing.T, asm string) string {
	t.Helper()
	start := strings.Index(asm, ".global __fern_udp_send\n")
	if start < 0 {
		t.Fatal("__fern_udp_send not emitted; the test cannot guard a helper that is absent")
	}
	rest := asm[start+1:]
	if end := strings.Index(rest, "\n.global "); end >= 0 {
		rest = rest[:end]
	}
	if end := strings.Index(rest, ".size __fern_udp_send"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// On Darwin the sockaddr_in family is the byte at offset 1. The Linux
// halfword store puts AF_INET in the length byte and leaves the family 0,
// which XNU refuses for a datagram connect with EAFNOSUPPORT. The behavioural
// half is TestArm64DarwinUdpSend in internal/e2e, which the macos-15 lane runs.
func TestArm64UdpSendSockaddrHead(t *testing.T) {
	darwin := udpSendBody(t, compile(t, udpOnlySrc, Options{Darwin: true}))
	if !strings.Contains(darwin, "strb w0, [x29, #81]") {
		t.Error("Darwin __fern_udp_send does not store the family byte at sockaddr offset 1")
	}
	if strings.Contains(darwin, "strh w0, [x29, #80]") {
		t.Error("Darwin __fern_udp_send writes Linux's halfword family, which XNU reads as family 0")
	}
	linux := udpSendBody(t, compile(t, udpOnlySrc, Options{}))
	if !strings.Contains(linux, "strh w0, [x29, #80]") {
		t.Error("Linux __fern_udp_send does not store the halfword sin_family")
	}
}

// udp_send has a gate of its own, so a datagram-only program links none of
// the TCP runtime.
func TestArm64UdpSendLinksNoTcpRuntime(t *testing.T) {
	asm := compile(t, udpOnlySrc, Options{})
	udpSendBody(t, asm)
	if strings.Contains(asm, "__fern_tcp_") {
		t.Error("a udp_send-only program links the TCP runtime")
	}
}
