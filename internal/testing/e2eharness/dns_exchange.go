package e2eharness

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type FakeNameserverMode int

const (
	FakeNameserverAnswer FakeNameserverMode = iota
	FakeNameserverTruncate
	FakeNameserverSilent
	FakeNameserverPair
	FakeNameserverAAAAOnly
	FakeNameserverNat64
)

type FakeNameserver struct {
	Port       int
	UDPQueries *int32
	TCPQueries *int32
}

func dnsQuestionType(msg []byte) int {
	qlen := dnsQuestionLen(msg)
	if qlen == 0 || 12+qlen > len(msg) {
		return 0
	}
	return int(msg[12+qlen-4])<<8 | int(msg[12+qlen-3])
}

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

func listenLoopbackPair(port int) (*net.UDPConn, *net.TCPListener, error) {
	for attempt := 0; ; attempt++ {
		udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
		if err != nil {
			return nil, nil, fmt.Errorf("listen udp: %w", err)
		}
		p := udp.LocalAddr().(*net.UDPAddr).Port
		tcp, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: p})
		if err == nil {
			return udp, tcp, nil
		}
		udp.Close()
		if !errors.Is(err, syscall.EADDRINUSE) || attempt == 20 {
			return nil, nil, fmt.Errorf("listen tcp on %d: %w", p, err)
		}
		port = 0
	}
}

func dnsReply(query []byte, truncated bool, mode FakeNameserverMode) []byte {
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
	if mode == FakeNameserverNat64 {
		out[7] = 1
		out = append(out, 192, 12, 0, 28, 0, 1, 0, 0, 0, 60, 0, 16, 0x00, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0, 0xc0, 0x00, 0x00, 0xaa)
		return out
	}
	out = append(out, 192, 12, 0, 5, 0, 1, 0, 0, 0, 60, 0, 6, 3, 'w', 'w', 'w', 192, 15)
	if dnsQuestionType(query) == 28 {
		out = append(out, 192, 44, 0, 28, 0, 1, 0, 0, 0, 60, 0, 16, 0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1)
		return out
	}
	out = append(out, 192, 44, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 93, 184, 216, 34)
	return out
}

func StartFakeNameserver(t *testing.T, mode FakeNameserverMode) FakeNameserver {
	t.Helper()
	udp, tcp, err := listenLoopbackPair(0)
	if err != nil {
		t.Fatal(err)
	}
	port := udp.LocalAddr().(*net.UDPAddr).Port
	var udpN, tcpN int32
	t.Cleanup(func() {
		udp.Close()
		tcp.Close()
	})
	seenAAAA := make(chan struct{})
	var closeSeen sync.Once
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
			query := append([]byte(nil), buf[:n]...)
			reply := dnsReply(query, mode == FakeNameserverTruncate, mode)
			if reply == nil {
				continue
			}
			if mode == FakeNameserverAAAAOnly && dnsQuestionType(query) != 28 {
				continue
			}
			if mode == FakeNameserverPair {
				if dnsQuestionType(query) == 28 {
					closeSeen.Do(func() { close(seenAAAA) })
				} else {
					go func() {
						<-seenAAAA
						_, _ = udp.WriteToUDP(reply, from)
					}()
					continue
				}
			}
			_, _ = udp.WriteToUDP(reply, from)
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
				reply := dnsReply(msg, false, mode)
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

func DnsExchangeSource(port int) string {
	return fmt.Sprintf(`import "std/dns";
import "std/time";
import "std/net";

function main(): i32 {
    let ns: net.SocketAddr = net.socket_addr(net.ipv4_loopback(), %d);
    let conf: dns.ResolvConf = dns.ResolvConf { nameservers: [ns], search: [], ndots: 1, timeout: time.duration_seconds(1 as i64), attempts: 1, rotate: false };
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

func DnsPairSource(port int) string {
	return fmt.Sprintf(`import "std/dns";
import "std/time";
import "std/net";

function main(): i32 {
    let ns: net.SocketAddr = net.socket_addr(net.ipv4_loopback(), %d);
    let conf: dns.ResolvConf = dns.ResolvConf { nameservers: [ns], search: [], ndots: 1, timeout: time.duration_seconds(1 as i64), attempts: 1, rotate: false };
    match (dns.lookup_addresses(conf, "vm.example.com.")) {
        Ok(xs) => {
            let i: i32 = 0;
            while (i < xs.len()) { print(xs[i].to_string()); i = i + 1; }
            return 0;
        },
        Err(e) => { print(e.message()); return 2; }
    }
}
`, port)
}

func CheckDnsPair(t *testing.T, ns FakeNameserver, stdout string, exit int) {
	t.Helper()
	got := strings.Fields(stdout)
	sort.Strings(got)
	if exit != 0 || strings.Join(got, " ") != "2001:db8::1 93.184.216.34" {
		t.Fatalf("exit %d, stdout %q; want exit 0 and both addresses", exit, stdout)
	}
	if udp := atomic.LoadInt32(ns.UDPQueries); udp != 2 {
		t.Errorf("the server saw %d UDP queries; want 2 (the A and the AAAA together)", udp)
	}
}

func DnsNat64Source(port int) string {
	return fmt.Sprintf(`import "std/dns";
import "std/time";
import "std/net";
import "std/i32";

function main(): i32 {
    let ns: net.SocketAddr = net.socket_addr(net.ipv4_loopback(), %d);
    let conf: dns.ResolvConf = dns.ResolvConf { nameservers: [ns], search: [], ndots: 1, timeout: time.duration_seconds(1 as i64), attempts: 1, rotate: false };
    let ps: dns.Nat64Prefix[] = dns.nat64_prefixes(conf);
    let i: i32 = 0;
    while (i < ps.len()) {
        match (net.ipv6(ps[i].bytes)) {
            Some(ip) => { print(ip.to_string() + "/" + ps[i].bits.to_string()); },
            None => { print("?"); }
        }
        i = i + 1;
    }
    return 0;
}
`, port)
}

func CheckDnsNat64(t *testing.T, stdout string, exit int) {
	t.Helper()
	if exit != 0 || stdout != "64:ff9b::/96\n" {
		t.Fatalf("exit %d, stdout %q; want exit 0 and 64:ff9b::/96", exit, stdout)
	}
}

func StartEchoListener(t *testing.T) int {
	t.Helper()
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				buf := make([]byte, 2)
				if _, err := readFull(c, buf); err == nil && string(buf) == "hi" {
					_, _ = c.Write([]byte("ok"))
				}
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func DnsDialSource(first string, port int) string {
	return fmt.Sprintf(`import "std/dns";
import "std/time";
import "std/net";

// recv reads once from fd within five seconds: the bytes, none at the end
// of the stream, when nothing came, or when the read failed.
function recv(fd: i32, max: i32): u8[] {
    match (net.read_watched(fd, max, 5000i64, 1)) {
        Ok(net.Chunk(b)) => { return b; },
        _ => {}
    }
    let none: u8[] = [];
    return none;
}

function main(): i32 {
    let first: net.IpAddr = net.ipv4_unspecified();
    match (net.ip_parse(%q)) {
        Some(ip) => { first = ip; },
        None => { return 3; }
    }
    let opts: dns.DialOptions = dns.DialOptions { fallback: time.duration_millis(200 as i64), timeout: time.duration_seconds(5 as i64) };
    match (dns.connect_race([first, net.ipv4_loopback()], %d, opts)) {
        Ok(sock) => {
            tcp_send_bytes(sock, [104u8, 105u8]);
            let got: u8[] = recv(sock, 16);
            print(string_from_bytes_unchecked(got));
            tcp_close(sock);
            return 0;
        },
        Err(e) => { print(e.message()); return 2; }
    }
}
`, first, port)
}

func CheckDnsDial(t *testing.T, stdout string, exit int) {
	t.Helper()
	if exit != 0 || stdout != "ok\n" {
		t.Fatalf("exit %d, stdout %q; want exit 0 and ok", exit, stdout)
	}
}
