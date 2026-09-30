package e2eharness

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The per-held-connection heap bound of #9853: a serve loop holding N idle
// connections must grow the heap by under 1 KiB per connection, measured
// by holding N and then 2N and reading the bump allocator's high-water
// mark through the server's own handler.

// HeldConnectionsBytesPerConnection is the bound: the bump-allocator growth
// the second batch of held connections may cost, per connection.
const HeldConnectionsBytesPerConnection = 1024

// HeldConnectionsBatch is N: the connections held per batch.
const HeldConnectionsBatch = 64

// HeldConnectionsServerSource is a server on `port` whose handler answers
// the bump allocator's high-water mark in bytes, with a read deadline long
// enough that the held connections stay open while it is measured and no
// per-client cap, since both batches come from this host.
func HeldConnectionsServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/time";
import "std/tcp";

function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.ok(__heap_bump_bytes().to_string());
}

function main(): i32 {
    var opts: tcp.ServeOptions = tcp.ServeOptions { ...tcp.serve_options(), recv_deadline: time.duration_seconds(120 as i64), max_connections_per_ip: 0 };
    return tcp.tcp_serve_opts(%d, opts, handle);
}
`, port)
}

// MeasureHeldConnections holds two batches of idle connections to the
// server at addr and answers the bump-allocator growth the second batch
// cost, per connection. The first batch's growth includes whatever the
// connection table and the loop allocate once (the first accept, the
// table's first growth), which is why the bound is on the second.
func MeasureHeldConnections(t *testing.T, addr string) (perConnection int64, first, second int64) {
	t.Helper()
	held := make([]net.Conn, 0, 2*HeldConnectionsBatch)
	defer func() {
		for _, c := range held {
			c.Close()
		}
	}()
	hold := func() {
		for i := 0; i < HeldConnectionsBatch; i++ {
			c, err := net.DialTimeout("tcp", addr, 5*time.Second)
			if err != nil {
				t.Fatalf("dial %d: %v", len(held), err)
			}
			held = append(held, c)
		}
	}
	// Settle: the loop has accepted the batch once a request through it
	// answers, since accepts and reads are served in one wait.
	before := heapBumpBytes(t, addr)
	hold()
	after1 := heapBumpBytes(t, addr)
	hold()
	after2 := heapBumpBytes(t, addr)
	first = after1 - before
	second = after2 - after1
	return second / HeldConnectionsBatch, first, second
}

// heapBumpBytes asks the server for its bump allocator's high-water mark.
func heapBumpBytes(t *testing.T, addr string) int64 {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		fmt.Fprintf(conn, "GET /heap HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n")
		r := bufio.NewReader(conn)
		status, err := r.ReadString('\n')
		if err != nil || !strings.HasPrefix(status, "HTTP/1.1 200") {
			conn.Close()
			t.Fatalf("heap probe answered %q: %v", status, err)
		}
		var body string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			if line == "\r\n" {
				rest, _ := r.ReadString('\n')
				body = strings.TrimSpace(rest)
				break
			}
		}
		conn.Close()
		n, err := strconv.ParseInt(body, 10, 64)
		if err != nil {
			t.Fatalf("heap probe body %q is not a number: %v", body, err)
		}
		return n
	}
	t.Fatalf("server at %s never answered the heap probe", addr)
	return 0
}

// BumpPerRequestServerSource is a single-loop server whose handler
// answers the bump allocator's high-water mark, `__heap_bump_bytes()`,
// with room for 5000 requests on one connection.
func BumpPerRequestServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.ok(__heap_bump_bytes().to_string());
}
function main(): i32 {
    return tcp.tcp_serve_opts(%d, tcp.ServeOptions { ...tcp.serve_options(), keep_alive_requests: 5000 }, handle);
}
`, port)
}

// CheckBumpPerRequest drives BumpPerRequestServerSource over one
// keep-alive connection: once the first requests have warmed the
// allocator's free lists, requests reuse what earlier ones freed, so the
// bump high-water mark reported at request 200 is the one reported at
// request 2000 (#9853's per-request gate, bump half).
func CheckBumpPerRequest(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	reader := bufio.NewReader(conn)
	var at200, last string
	for i := 1; i <= 2000; i++ {
		if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Fatalf("response %d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		last = string(body)
		if i == 200 {
			at200 = last
		}
	}
	if last != at200 {
		t.Fatalf("bump high-water mark %s bytes at request 200 but %s at request 2000: a keep-alive request grows the heap", at200, last)
	}
}
