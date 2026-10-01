package e2eharness

import (
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// FakeNameserverMode is how StartFakeNameserver answers a query for
// vm.example.com: Answer replies over UDP with the CNAME and A record,
// Truncate replies over UDP with the TC bit and no answer so the client
// asks again over TCP, where the full reply waits, and Silent never
// replies at all.
type FakeNameserverMode int

const (
	FakeNameserverAnswer FakeNameserverMode = iota
	FakeNameserverTruncate
	FakeNameserverSilent
)

// FakeNameserver is a nameserver on the loopback interface for one name:
// its port, and how many queries it took over each transport.
type FakeNameserver struct {
	Port       int
	UDPQueries *int32
	TCPQueries *int32
}

// dnsQuestionLen is the length of the question at offset 12: the labels
// up to the root, then type and class.
func dnsQuestionLen(msg []byte) int {
	p := 12
	for p < len(msg) {
		l := int(msg[p])
		if l == 0 {
			return p + 1 + 4 - 12
		}
		p += 1 + l
	}
	return 0
}

// dnsReply is the reply to a query for vm.example.com: the question
// echoed, then (unless truncated) a CNAME to www.example.com and the A
// record 93.184.216.34, both owners compressed back to the question,
// which has to be `vm.example.com` for the pointers to land.
func dnsReply(query []byte, truncated bool) []byte {
	qlen := dnsQuestionLen(query)
	if qlen == 0 || qlen > len(query)-12 {
		return nil
	}
	flags := []byte{0x81, 0x80}
	an := byte(2)
	if truncated {
		flags = []byte{0x83, 0x80}
		an = 0
	}
	out := []byte{query[0], query[1], flags[0], flags[1], 0, 1, 0, an, 0, 0, 0, 0}
	out = append(out, query[12:12+qlen]...)
	if truncated {
		return out
	}
	out = append(out, 192, 12, 0, 5, 0, 1, 0, 0, 0, 60, 0, 6, 3, 'w', 'w', 'w', 192, 15)
	out = append(out, 192, 44, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 93, 184, 216, 34)
	return out
}

// StartFakeNameserver listens on a free loopback port over UDP and TCP
// and answers as `mode` says until the test ends.
func StartFakeNameserver(t *testing.T, mode FakeNameserverMode) FakeNameserver {
	t.Helper()
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	port := udp.LocalAddr().(*net.UDPAddr).Port
	tcp, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		udp.Close()
		t.Fatalf("listen tcp on %d: %v", port, err)
	}
	var udpN, tcpN int32
	t.Cleanup(func() {
		udp.Close()
		tcp.Close()
	})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, from, err := udp.ReadFromUDP(buf)
			if err != nil {
				return
			}
			atomic.AddInt32(&udpN, 1)
			if mode == FakeNameserverSilent {
				continue
			}
			if reply := dnsReply(buf[:n], mode == FakeNameserverTruncate); reply != nil {
				_, _ = udp.WriteToUDP(reply, from)
			}
		}
	}()
	go func() {
		for {
			c, err := tcp.Accept()
			if err != nil {
				return
			}
			atomic.AddInt32(&tcpN, 1)
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				head := make([]byte, 2)
				if _, err := readFull(c, head); err != nil {
					return
				}
				msg := make([]byte, int(head[0])<<8|int(head[1]))
				if _, err := readFull(c, msg); err != nil {
					return
				}
				if mode == FakeNameserverSilent {
					return
				}
				reply := dnsReply(msg, false)
				framed := append([]byte{byte(len(reply) >> 8), byte(len(reply))}, reply...)
				_, _ = c.Write(framed)
			}(c)
		}
	}()
	return FakeNameserver{Port: port, UDPQueries: &udpN, TCPQueries: &tcpN}
}

func readFull(c net.Conn, buf []byte) (int, error) {
	got := 0
	for got < len(buf) {
		n, err := c.Read(buf[got:])
		got += n
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

// DnsExchangeSource is a program that asks the nameserver at `port` on
// the loopback interface for vm.example.com's address and canonical name
// through std/dns, with one attempt and a one-second timeout: it prints
// the address and the name and exits 0, or prints the failure and exits
// 2 (the address) or 3 (the name).
func DnsExchangeSource(port int) string {
	return fmt.Sprintf(`import "std/dns";
import "std/net";

function main(): i32 {
    var ns: net.SocketAddr = net.socket_addr(net.ipv4_loopback(), %d);
    var conf: dns.ResolvConf = dns.ResolvConf { nameservers: [ns], search: [], ndots: 1, timeout_ms: 1000, attempts: 1, rotate: false };
    match (dns.lookup_a(conf, "vm.example.com.")) {
        Ok(xs) => { print(xs[0].to_string()); },
        Err(e) => { print(e.message()); return 2; }
    }
    match (dns.canonical_name(conf, "vm.example.com.")) {
        Ok(n) => { print(n); return 0; },
        Err(e) => { print(e.message()); return 3; }
    }
}
`, port)
}

// CheckDnsExchange compares a DnsExchangeSource run against `mode`: the
// address and the name on stdout with exit 0 for a server that answers,
// the silence reported with exit 2 for one that does not; and the
// transports the server saw: UDP alone, UDP then TCP when it truncated.
func CheckDnsExchange(t *testing.T, mode FakeNameserverMode, ns FakeNameserver, stdout string, exit int) {
	t.Helper()
	udp := atomic.LoadInt32(ns.UDPQueries)
	tcp := atomic.LoadInt32(ns.TCPQueries)
	switch mode {
	case FakeNameserverSilent:
		if exit != 2 || stdout != "Temporary failure in name resolution\n" {
			t.Fatalf("a silent nameserver: exit %d, stdout %q; want exit 2 and the silence reported", exit, stdout)
		}
		if udp == 0 || tcp != 0 {
			t.Errorf("a silent nameserver saw %d UDP and %d TCP queries; want some UDP and no TCP", udp, tcp)
		}
	default:
		if exit != 0 || stdout != "93.184.216.34\nwww.example.com\n" {
			t.Fatalf("exit %d, stdout %q; want exit 0, the address and the canonical name", exit, stdout)
		}
		if udp != 2 {
			t.Errorf("the server saw %d UDP queries; want 2 (one per lookup)", udp)
		}
		wantTCP := int32(0)
		if mode == FakeNameserverTruncate {
			wantTCP = 2
		}
		if tcp != wantTCP {
			t.Errorf("the server saw %d TCP queries; want %d", tcp, wantTCP)
		}
	}
}
