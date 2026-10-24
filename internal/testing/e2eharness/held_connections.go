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

const HeldConnectionsBytesPerConnection = 1024

const HeldConnectionsBatch = 64

func HeldConnectionsServerSource() string {
	return heldConnectionsServer("", "serve.config()", `function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok(__heap_bump_bytes().to_string());
}`)
}

func heldConnectionsServer(imports, config, handler string) string {
	return fmt.Sprintf(`import "std/http";
import "std/time";
import "std/serve";
import "std/platform";
%s
%s

function main(): i32 {
    let opts: serve.Config = serve.Config { ...%s, recv_deadline: time.duration_seconds(120 as i64), max_connections_per_ip: 0, max_in_flight: %d };
    return serve.run(0, opts, handle);
}
`, imports, handler, config, 4*HeldConnectionsBatch)
}

const HeldSuspendedBytesPerHandler = 8192

func HeldSuspendedServerSource() string {
	return heldConnectionsServer(`import "std/fetch";`, "serve.config()", heldSuspendedHandler)
}

const heldSuspendedHandler = `function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/heap") {
        return http.ok(__heap_bump_bytes().to_string());
    }
    let parked: fetch.Timeouts = fetch.Timeouts { ...fetch.timeouts(), inactivity: time.duration_seconds(300 as i64), total: time.duration_seconds(600 as i64) };
    match (plat.http(fetch.get("http://8.8.8.8/hold").with_timeouts(parked))) {
        Ok(resp) => { return http.ok("held " + resp.status.to_string()); },
        Err(e) => { return http.ok("held err " + e.message()); }
    }
}`

const HeldStreamsBytesPerStream = 12288

func HeldStreamsServerSource() string {
	return heldConnectionsServer(`import "std/fetch";`, "serve.config().with_h2()", heldSuspendedHandler)
}

func MeasureHeldStreams(t *testing.T, addr string, up *FetchUpstream) (perStream int64, first, second int64) {
	t.Helper()
	client, err := DialH2C(addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Minute))
	streams := make([]uint32, 0, 2*HeldConnectionsBatch)
	hold := func() {
		for i := 0; i < HeldConnectionsBatch; i++ {
			stream, err := client.Open("/park")
			if err != nil {
				t.Fatalf("stream %d: %v", len(streams)+1, err)
			}
			streams = append(streams, stream)
		}
		deadline := time.Now().Add(30 * time.Second)
		for up.Held() < len(streams) {
			if time.Now().After(deadline) {
				t.Fatalf("%d handlers parked on the upstream after 30s, want %d", up.Held(), len(streams))
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
	for i, stream := range streams {
		body, err := client.Await(stream)
		if err != nil {
			t.Fatalf("stream %d after release: %v", i+1, err)
		}
		if body != "held 200" {
			t.Fatalf("stream %d answered %q after release, want \"held 200\"", i+1, body)
		}
	}
	return second / HeldConnectionsBatch, first, second
}

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
	before := heapBumpBytes(t, addr)
	hold()
	after1 := heapBumpBytes(t, addr)
	hold()
	after2 := heapBumpBytes(t, addr)
	first = after1 - before
	second = after2 - after1
	return second / HeldConnectionsBatch, first, second
}

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

func BumpPerRequestServerSource() string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok(__heap_bump_bytes().to_string());
}
function main(): i32 {
    return serve.run(0, serve.Config { ...serve.config(), keep_alive_requests: %d }, handle);
}
`, 2*BumpPerRequestRounds+1)
}

const BumpPerRequestRounds = 100000

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
	var atWarm, last, firstDate string
	warmed, end := 0, rounds
	for i := 1; i <= end; i++ {
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
		date := resp.Header.Get("Date")
		if date == "" {
			t.Fatalf("response %d has no Date", i)
		}
		if i == 1 {
			firstDate = date
		}
		if warmed == 0 && i >= warm && date != firstDate {
			warmed, atWarm = i, last
			end = i + rounds - warm
		}
		if warmed == 0 && i == end {
			if end == 2*rounds {
				t.Fatalf("no Date change in %d requests", end)
			}
			end++
		}
	}
	if last != atWarm {
		t.Fatalf("bump high-water mark %s bytes at request %d but %s at request %d: a keep-alive request grows the heap", atWarm, warmed, last, end)
	}
}
