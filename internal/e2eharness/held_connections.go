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

// The per-held-connection heap bound of #9853 and #9854: a serve loop
// holding N idle connections must grow the heap by under 1 KiB per
// connection, measured by holding N and then 2N and reading the bump
// allocator's high-water mark through the server's own handler. A
// connection is held in one of two shapes: accepted and never used, or
// kept alive after one served request.

// HeldConnectionsBytesPerConnection is the bound: the bump-allocator growth
// the second batch of held connections may cost, per connection.
const HeldConnectionsBytesPerConnection = 1024

// HeldConnectionsBatch is N: the connections held per batch.
const HeldConnectionsBatch = 64

// HeldConnectionsServerSource is a server on `port` whose handler answers
// the bump allocator's high-water mark in bytes.
func HeldConnectionsServerSource(port int) string {
	return heldConnectionsServer(port, "", `function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok(__heap_bump_bytes().to_string());
}`)
}

// heldConnectionsServer is the held-connection servers' shared shape:
// `handler` serving on `port` under a read deadline long enough that an
// idle held connection stays open while it is measured (a parked one
// holds under the loop's own held deadline instead) and no per-client cap,
// since both batches come from this host and the default cap would close
// the second on accept.
func heldConnectionsServer(port int, imports, handler string) string {
	return fmt.Sprintf(`import "std/http";
import "std/time";
import "std/serve";
import "std/platform";
%s
%s

function main(): i32 {
    let opts: serve.Config = serve.Config { ...serve.config(), recv_deadline: time.duration_seconds(120 as i64), max_connections_per_ip: 0 };
    return serve.run(%d, opts, handle);
}
`, imports, handler, port)
}

// HeldSuspendedBytesPerHandler is the bound on the bump-allocator growth
// the second batch of held connections may cost, per connection, when
// each connection's handler is parked on its upstream: the connection's
// own cost plus the flight, the task's record and save area, and the
// fetch client's request, pooled connection and parked frames. The
// self-host's loop costs about 12 KiB.
const HeldSuspendedBytesPerHandler = 16384

// HeldSuspendedServerSource is HeldConnectionsServerSource with a handler
// that waits on `plat.http` to the fetch upstream's /hold target (through
// the forward proxy SetFetchProxy names) for every path but /heap, so a
// connection with a request in flight holds a suspended handler. The
// request's bounds sit far above the measurement's own guards, so a
// handler that answers early is the upstream's doing, never the client
// giving up on the park.
func HeldSuspendedServerSource(port int) string {
	return heldConnectionsServer(port, `import "std/fetch";`, `function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/heap") {
        return http.ok(__heap_bump_bytes().to_string());
    }
    let parked: fetch.Timeouts = fetch.Timeouts { ...fetch.timeouts(), inactivity_ms: 300000, total_ms: 600000 };
    match (plat.http(fetch.get("http://8.8.8.8/hold").with_timeouts(parked))) {
        Ok(resp) => { return http.ok("held " + resp.status.to_string()); },
        Err(e) => { return http.ok("held err " + e.message()); }
    }
}`)
}

// MeasureHeldSuspended holds two batches of connections to the server at
// addr, each with a request whose handler is parked on the upstream's
// /hold target, and answers the bump-allocator growth the second batch
// cost, per suspended handler. A batch counts as held once the upstream
// has every handler's request parked. The upstream is then released and
// every held connection must answer its request, so the parked handlers
// are shown to resume as well as to fit.
func MeasureHeldSuspended(t *testing.T, addr string, up *FetchUpstream) (perHandler int64, first, second int64) {
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
			if _, err := io.WriteString(c, "GET /park HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
				t.Fatalf("request on connection %d: %v", len(held), err)
			}
		}
		deadline := time.Now().Add(30 * time.Second)
		for up.Held() < len(held) {
			if time.Now().After(deadline) {
				t.Fatalf("%d handlers parked on the upstream after 30s, want %d", up.Held(), len(held))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	before := heapBumpBytes(t, addr)
	hold()
	after1 := heapBumpBytes(t, addr)
	hold()
	after2 := heapBumpBytes(t, addr)
	first = after1 - before
	second = after2 - after1
	up.Release()
	for i, c := range held {
		_ = c.SetDeadline(time.Now().Add(30 * time.Second))
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			t.Fatalf("response on held connection %d after release: %v", i+1, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "held 200" {
			t.Fatalf("held connection %d answered %d %q after release, want 200 \"held 200\"", i+1, resp.StatusCode, body)
		}
	}
	return second / HeldConnectionsBatch, first, second
}

// MeasureHeldConnections holds two batches of idle connections to the
// server at addr and answers the bump-allocator growth the second batch
// cost, per connection. With served, each connection carries one request
// and its response before it is left idle, so it is held as a kept-alive
// connection; without, it is accepted and never used. The first batch's
// growth includes whatever the connection table and the loop allocate
// once (the first accept, the table's first growth), which is why the
// bound is on the second.
func MeasureHeldConnections(t *testing.T, addr string, served bool) (perConnection int64, first, second int64) {
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
			if served {
				serveOnce(t, c, len(held))
			}
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

// serveOnce sends one request on c and reads its response whole, leaving
// the connection open on keep-alive.
func serveOnce(t *testing.T, c net.Conn, n int) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Fatalf("request on connection %d: %v", n, err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("response on connection %d: %v", n, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Close {
		t.Fatalf("connection %d answered %d, close=%v: want a 200 kept alive", n, resp.StatusCode, resp.Close)
	}
	_ = c.SetDeadline(time.Time{})
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
// with room for BumpPerRequestRounds requests on one connection.
func BumpPerRequestServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok(__heap_bump_bytes().to_string());
}
function main(): i32 {
    return serve.run(%d, serve.Config { ...serve.config(), keep_alive_requests: %d }, handle);
}
`, port, BumpPerRequestRounds+1)
}

// BumpPerRequestRounds is #9853's per-request count: 100k requests on
// one keep-alive connection.
const BumpPerRequestRounds = 100000

// CheckBumpPerRequest drives BumpPerRequestServerSource over one
// keep-alive connection for `rounds` requests: once the first tenth have
// warmed the allocator's free lists, requests reuse what earlier ones
// freed, so the bump high-water mark reported a tenth of the way in is
// the one reported at the last request (#9853's per-request gate, bump
// half).
func CheckBumpPerRequest(t *testing.T, addr string, rounds int) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))
	reader := bufio.NewReader(conn)
	warm := rounds / 10
	var atWarm, last string
	for i := 1; i <= rounds; i++ {
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
		if i == warm {
			atWarm = last
		}
	}
	if last != atWarm {
		t.Fatalf("bump high-water mark %s bytes at request %d but %s at request %d: a keep-alive request grows the heap", atWarm, warm, last, rounds)
	}
}
