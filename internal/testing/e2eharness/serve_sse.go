package e2eharness

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func SSEServerSource() string {
	return `import "std/http";
import "std/serve";
import "std/platform";
import "std/time";
import "std/http/sse";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let ticks: (i32) => Option[sse.Event] = (i: i32): Option[sse.Event] => {
        if (i >= 3) { return None; }
        plat.sleep(time.duration_millis(250));
        return Some(sse.event("tick " + i.to_string()).with_id(i.to_string()));
    };
    if (req.path == "/events") { return sse.respond_with(time.duration_millis(100), ticks); }
    if (req.path == "/plain") { return sse.respond(ticks); }
    return http.ok("ok");
}
function main(): i32 {
    let opts: serve.Config = serve.Config { ...serve.config(), workers: 1 };
    return serve.supervise(0, opts, handle);
}
`
}

func sseEvents(t *testing.T, addr, path string) (status string, data []string, comments int) {
	t.Helper()
	c := dialRaw(t, addr)
	defer c.Close()
	if _, err := fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: h\r\nConnection: close\r\n\r\n", path); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(c)
	status, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read the status line of %s: %v", path, err)
	}
	status = strings.TrimSpace(status)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read the headers of %s: %v", path, err)
		}
		if line == "\r\n" {
			break
		}
	}
	for {
		size, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read a chunk size of %s: %v", path, err)
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(size), "%x", &n); err != nil {
			t.Fatalf("chunk size %q of %s: %v", size, path, err)
		}
		if n == 0 {
			return status, data, comments
		}
		chunk := make([]byte, n+2)
		for got := 0; got < len(chunk); {
			m, err := r.Read(chunk[got:])
			if err != nil {
				t.Fatalf("read a chunk of %s: %v", path, err)
			}
			got += m
		}
		for _, line := range strings.Split(string(chunk[:n]), "\n") {
			switch {
			case strings.HasPrefix(line, "data: "):
				data = append(data, strings.TrimPrefix(line, "data: "))
			case strings.HasPrefix(line, ": "):
				comments++
			}
		}
	}
}

func CheckSSEOverlap(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	c := dialRaw(t, addr)
	defer c.Close()
	if _, err := fmt.Fprintf(c, "GET /events HTTP/1.1\r\nHost: h\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	started := time.Now()
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/ok beside a stream in progress: want 200, got\n%s", resp)
	}
	if waited := time.Since(started); waited > 500*time.Millisecond {
		t.Fatalf("/ok beside a stream took %v: the worker stalled on the producer", waited)
	}
	status, data, comments := sseEvents(t, addr, "/events")
	if status != "HTTP/1.1 200 OK" || strings.Join(data, ",") != "tick 0,tick 1,tick 2" {
		t.Fatalf("/events: want 200 with three ticks, got %s %v", status, data)
	}
	if comments < 3 {
		t.Fatalf("/events carried %d keep-alive comments, want at least 3 over three 250 ms waits", comments)
	}
}

func CheckSSESequential(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	for _, path := range []string{"/events", "/plain"} {
		status, data, _ := sseEvents(t, addr, path)
		if status != "HTTP/1.1 200 OK" || strings.Join(data, ",") != "tick 0,tick 1,tick 2" {
			t.Fatalf("%s: want 200 with three ticks, got %s %v", path, status, data)
		}
	}
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/ok after the streams: want 200, got\n%s", resp)
	}
}

var _ net.Conn = (*net.TCPConn)(nil)
