// HTTP/TCP deadlines (#4385) — e2e tests for the read-deadline
// surface on the native x86-64 backend:
//
//  1. `tcp_serve_deadline`'s slow-loris guard: a client that
//     connects and trickles a partial header is disconnected at
//     the per-request read deadline WITHOUT a response, and the
//     single-threaded accept loop is not pinned — a well-formed
//     request right after still answers 200 (the scenario is
//     e2eharness's, shared with the self-host twin).
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
	"net"
	"os/exec"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestServeRecvDeadlineX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.RecvDeadlineServerSource(port))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckRecvDeadline(t, fmt.Sprintf("127.0.0.1:%d", port))
}

const fetchDeadlineSrc = `
import "std/fetch";
import "std/time";
function main(): i32 {
    // Silent upstream (accepts, never replies): must time out to None.
    var slow: Option[u8[]] = fetch.fetch_get_deadline(fetch.ipv4(127,0,0,1), %d, "/", time.duration_millis(400));
    var slow_ok: boolean = false;
    match (slow) {
        Some(s) => { },
        None => { slow_ok = true; },
    }
    if (!slow_ok) { return 1; }
    // Live upstream: must resolve in time with a 200 status line.
    var fast: Option[u8[]] = fetch.fetch_get_deadline(fetch.ipv4(127,0,0,1), %d, "/", time.duration_millis(5000));
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
