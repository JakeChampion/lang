package e2eharness

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// The serve loop's own allocations per request, #9853 exit criterion A's
// count half: a hello handler served on one keep-alive connection, with
// /count answering __heap_alloc_count(). The framing gate counts the parse
// and the serialize alone; this one counts everything the loop does between
// two requests (the reactor wait, the read, the handler's task, the reply,
// the buffer's compaction), and the handler's own response.

// ServeAllocsRounds is how many hello requests the count spans on a host
// running the server natively; ServeAllocsRoundsEmulated is the count under
// qemu or wasmtime, where a request costs about a millisecond.
const ServeAllocsRounds = 100000
const ServeAllocsRoundsEmulated = 10000

// serveAllocsWarm is how many requests run before the first count, so the
// connection's buffers and the loop's tables have reached their size.
const serveAllocsWarm = 2000

// serveAllocsH2Drift is how far under its ceiling the HTTP/2 mean may sit
// before the ceiling is lowered: three runs of the same build read 99.0 to
// 100.0, so a ceiling tracks the mean to within a few allocations.
const serveAllocsH2Drift = 4

// serveAllocsSlackPerSecond is how far the total may sit from a whole
// number per request, per second the rounds took and two more: the loop
// reformats its Date once a second, about thirty allocations each time.
const serveAllocsSlackPerSecond = 40

// ServeAllocsServerSource is a server answering "hello" to every
// path but /count, which answers the allocator's call count. It serves the
// listener it inherits, or `port` where it inherits none, for `rounds`
// requests and the warm-up on one connection.
func ServeAllocsServerSource(port, rounds int) string {
	return serveAllocsSource(port, rounds, "serve.config()")
}

// ServeAllocsH2ServerSource is ServeAllocsServerSource under `with_h2()`,
// for the requests to arrive on one HTTP/2 connection.
func ServeAllocsH2ServerSource(port, rounds int) string {
	return serveAllocsSource(port, rounds, "serve.config().with_h2()")
}

func serveAllocsSource(port, rounds int, config string) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/count") {
        return http.ok(__heap_alloc_count().to_string());
    }
    return http.ok("hello");
}
function main(): i32 {
    return serve.run(%d, serve.Config { ...%s, keep_alive_requests: %d }, handle);
}
`, port, config, serveAllocsWarm+rounds+8)
}

// CheckServeAllocs drives ServeAllocsServerSource at addr for rounds hello
// requests and holds the allocations per request to want. Two counts back
// to back give what a count request costs, which is taken off the count
// around the rounds. The pin is a ratchet: above it is a regression, below
// it lowers the pin.
func CheckServeAllocs(t *testing.T, addr string, want int64, rounds int) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
	reader := bufio.NewReader(conn)
	get := func(path string) string {
		if _, err := fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: localhost:8080\r\nUser-Agent: curl/8.5.0\r\nAccept: */*\r\n\r\n", path); err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s answered %d", path, resp.StatusCode)
		}
		return string(body)
	}
	total, seconds := serveAllocsRounds(t, get, rounds)
	got := int64(math.Round(float64(total) / float64(rounds)))
	if d := total - got*int64(rounds); d < -serveAllocsSlackPerSecond*seconds || d > serveAllocsSlackPerSecond*seconds {
		t.Fatalf("%d allocations over %d requests is %d from a whole number per request", total, rounds, d)
	}
	if got != want {
		t.Errorf("the serve loop allocates %d times per hello request; pinned %d.\n"+
			"Above the pin is a regression. Below it, lower the pin in the same change.", got, want)
	}
}

// CheckServeAllocsH2 is CheckServeAllocs over one prior-knowledge HTTP/2
// connection (H2CClient), against ServeAllocsH2ServerSource. The count
// per request varies there, with the blocks the connection's frames,
// header blocks and stream slots take and give back and with how the
// client's frames coalesce into reads, so `ceiling` bounds the mean: above
// it is a regression, and a mean serveAllocsH2Drift or more under it
// lowers the ceiling.
func CheckServeAllocsH2(t *testing.T, addr string, ceiling int64, rounds int) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	client, err := DialH2C(addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Minute))
	get := func(path string) string {
		body, err := client.Get(path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return body
	}
	total, seconds := serveAllocsRounds(t, get, rounds)
	mean := float64(total) / float64(rounds)
	if total > ceiling*int64(rounds)+serveAllocsSlackPerSecond*seconds {
		t.Errorf("the serve loop allocates %.2f times per HTTP/2 hello request; the ceiling is %d. Above it is a regression.", mean, ceiling)
	}
	if total < (ceiling-serveAllocsH2Drift)*int64(rounds) {
		t.Errorf("the serve loop allocates %.2f times per HTTP/2 hello request, %d or more under the ceiling of %d: lower the ceiling in the same change.", mean, serveAllocsH2Drift, ceiling)
	}
}

// serveAllocsRounds warms the connection `get` speaks over and counts
// around `rounds` hello requests: the allocations they cost, with what the
// count requests themselves cost taken off, and the seconds they took plus
// two, the loop's Date reformatting about thirty allocations once a second.
func serveAllocsRounds(t *testing.T, get func(path string) string, rounds int) (total, seconds int64) {
	t.Helper()
	count := func() int64 {
		n, err := strconv.ParseInt(get("/count"), 10, 64)
		if err != nil {
			t.Fatalf("/count: %v", err)
		}
		return n
	}
	for i := 0; i < serveAllocsWarm; i++ {
		if body := get("/"); body != "hello" {
			t.Fatalf("hello request %d answered %q", i, body)
		}
	}
	c0 := count()
	c1 := count()
	started := time.Now()
	for i := 0; i < rounds; i++ {
		get("/")
	}
	c2 := count()
	seconds = int64(time.Since(started)/time.Second) + 2
	total = c2 - c1 - (c1 - c0)
	t.Logf("counts %d, %d, %d: %d allocations over %d requests in %d s", c0, c1, c2, total, rounds, seconds-2)
	return total, seconds
}
