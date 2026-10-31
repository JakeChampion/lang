package e2eharness

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func PerIPCapServerSource() string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    return serve.exit_code(serve.supervise(0, serve.Config { ...serve.config(), workers: 1, max_connections_per_ip: 2 }, handle));
}
`)
}

func CheckPerIPCap(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr)
	held := make([]net.Conn, 0, 2)
	for n := 0; n < 2; n++ {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		held = append(held, conn)
	}
	time.Sleep(200 * time.Millisecond)

	third, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	_, _ = io.WriteString(third, "GET /ok HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
	if err := third.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(third)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("the third connection of one client stayed open past the cap of 2: %q", b)
	}
	if strings.Contains(string(b), "HTTP/1.1") {
		t.Fatalf("the third connection of one client was answered past the cap of 2:\n%s", b)
	}

	held[0].Close()
	time.Sleep(200 * time.Millisecond)
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !ContainsStatus200(resp) {
		t.Fatalf("a connection after one of the two closed was not served:\n%s", resp)
	}
	if _, err := io.WriteString(held[1], "GET /ok HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if err := held[1].SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(held[1]); !ContainsStatus200(string(b)) {
		t.Fatalf("the second of the two held connections was not answered:\n%s", b)
	}
}

// perIPPeers is enough loopback peers to fill a 16-slot count table past its
// load limit, so the table grows and its probe runs collide.
const perIPPeers = 40

func perIPDial(t *testing.T, addr string, peer int) net.Conn {
	t.Helper()
	d := net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, byte(2+peer))}}
	c, err := d.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("peer %d: %v", peer, err)
	}
	return c
}

func perIPServed(t *testing.T, c net.Conn) bool {
	t.Helper()
	defer c.Close()
	_, _ = io.WriteString(c, "GET /ok HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
	if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(c)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("a connection was neither answered nor closed: %q", b)
	}
	return ContainsStatus200(string(b))
}

// CheckPerIPCapManyPeers holds two connections from each of perIPPeers
// loopback addresses against a cap of 2. It then closes every odd peer's
// connections, which empties their count slots, and checks that each peer
// keeps its own count.
func CheckPerIPCapManyPeers(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr)
	held := make([][]net.Conn, perIPPeers)
	defer func() {
		for _, cs := range held {
			for _, c := range cs {
				c.Close()
			}
		}
	}()
	for p := 0; p < perIPPeers; p++ {
		held[p] = []net.Conn{perIPDial(t, addr, p), perIPDial(t, addr, p)}
	}
	time.Sleep(200 * time.Millisecond)
	for p := 0; p < perIPPeers; p++ {
		if perIPServed(t, perIPDial(t, addr, p)) {
			t.Fatalf("peer %d: a third connection was answered past the cap of 2", p)
		}
	}
	for p := 1; p < perIPPeers; p += 2 {
		for _, c := range held[p] {
			c.Close()
		}
		held[p] = nil
	}
	time.Sleep(200 * time.Millisecond)
	for p := 0; p < perIPPeers; p++ {
		served := perIPServed(t, perIPDial(t, addr, p))
		if p%2 == 0 && served {
			t.Fatalf("peer %d: a third connection was answered after other peers closed theirs", p)
		}
		if p%2 == 1 && !served {
			t.Fatalf("peer %d: a connection after its two closed was not answered", p)
		}
	}
}
