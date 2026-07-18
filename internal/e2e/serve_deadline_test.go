// HTTP/TCP deadlines (#4385) — e2e tests for the read-deadline
// surface on the native x86-64 backend:
//
//  1. `tcp_serve_deadline`'s slow-loris guard: a client that
//     connects and trickles a partial header is disconnected at
//     the per-request read deadline WITHOUT a response, and the
//     single-threaded accept loop is not pinned — a well-formed
//     request right after still answers 200.
//  2. `fetch_get_deadline`: against an upstream that accepts and
//     never replies the call returns `None` at the deadline;
//     against a live upstream it returns `Some(response)`.
//
// The interp fallback (poll is a stub there, so the deadline
// degrades to blocking reads) is covered by
// TestSupervisedServeInterpFallback, which serves through the
// same deadline-gated loop under -interp.
package e2e

import (
	"fmt"
	"io"
	"net"
	"os/exec"
	"testing"
	"time"
)

const serveDeadlineSrc = `
import "std/http";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.http_response_ok("ok");
}
function main(): i32 {
    return tcp.tcp_serve_deadline(%d, handle, 400);
}`

func TestServeRecvDeadlineX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, fmt.Sprintf(serveDeadlineSrc, port))
	_, _ = startSupervisedServer(t, bin, runner)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	waitServerReady(t, addr, 10*time.Second)

	// Slow-loris: send a partial header, never finish. The server must
	// close the connection at the ~400ms deadline (no response bytes),
	// well before our own 10s client-side guard.
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	start := time.Now()
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost:")); err != nil {
		t.Fatalf("partial write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	got, _ := io.ReadAll(conn) // EOF when the server closes us
	elapsed := time.Since(start)
	conn.Close()
	if len(got) != 0 {
		t.Fatalf("slow client got a response to an incomplete request: %q", got)
	}
	if elapsed >= 8*time.Second {
		t.Fatalf("server never enforced the read deadline (waited %v)", elapsed)
	}

	// The accept loop must not be pinned: a well-formed request right
	// after the timed-out one still answers 200.
	resp := httpRoundTrip(t, addr, "/ok", 3*time.Second)
	if !containsStatus200(resp) {
		t.Fatalf("request after a timed-out connection did not answer 200:\n%s", resp)
	}
}

func containsStatus200(resp string) bool {
	return len(resp) >= 15 && resp[:15] == "HTTP/1.1 200 OK"
}

const fetchDeadlineSrc = `
import "std/fetch";
function main(): i32 {
    // Silent upstream (accepts, never replies): must time out to None.
    var slow: Option[string] = fetch.fetch_get_deadline(fetch.ipv4(127,0,0,1), %d, "/", 400);
    var slow_ok: boolean = false;
    match (slow) {
        Some(s) => { },
        None => { slow_ok = true; },
    }
    if (!slow_ok) { return 1; }
    // Live upstream: must resolve in time with a 200 status line.
    var fast: Option[string] = fetch.fetch_get_deadline(fetch.ipv4(127,0,0,1), %d, "/", 5000);
    match (fast) {
        Some(resp) => {
            if (fetch.http_status(resp) == 200) { return 0; }
            return 2;
        },
        None => { return 3; },
    }
    return 4;
}`

func TestFetchDeadlineX86_64(t *testing.T) {
	// Silent upstream: accept and hold the connection open, never reply.
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback listener: %v", err)
	}
	defer silent.Close()
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()

	// Live upstream: one canned 200 per connection.
	live, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback listener: %v", err)
	}
	defer live.Close()
	go func() {
		for {
			c, err := live.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 4096)
				_, _ = c.Read(buf)
				_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nhi"))
				c.Close()
			}(c)
		}
	}()

	silentPort := silent.Addr().(*net.TCPAddr).Port
	livePort := live.Addr().(*net.TCPAddr).Port
	bin, runner := buildSupervisedServeBin(t, fmt.Sprintf(fetchDeadlineSrc, silentPort, livePort))

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("fetch deadline client failed (elapsed %v): %v\n%s", elapsed, err, out)
	}
	// The silent leg must actually have timed out at ~400ms, not hung.
	if elapsed >= 30*time.Second {
		t.Fatalf("fetch deadline client took %v — deadline not enforced", elapsed)
	}
}
