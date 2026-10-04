package e2eharness

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The serve-loop scenarios internal/e2e runs on the native backends and
// internal/e2eselfhost runs through the self-host compiler: each is a
// server program and the client-side check against a started process.

// HTTPRoundTrip sends one GET on a fresh connection, asking for the
// close, and returns whatever bytes came back ("" for a reset or no
// response).
func HTTPRoundTrip(t *testing.T, addr, path string, timeout time.Duration) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		t.Fatalf("dial for %s: %v", path, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", path)
	if _, err := conn.Write([]byte(req)); err != nil {
		// The write can race a worker's death; that is no response,
		// the same as a reset on the read side.
		return ""
	}
	resp, _ := io.ReadAll(conn)
	return string(resp)
}

// ContainsStatus200 reports whether resp starts with a 200 status line.
func ContainsStatus200(resp string) bool {
	return strings.HasPrefix(resp, "HTTP/1.1 200 OK")
}

// ResponseBodyTail is the body of an HTTP/1.1 response, or "" when the
// response has no header terminator (a closed connection).
func ResponseBodyTail(resp string) string {
	i := strings.Index(resp, "\r\n\r\n")
	if i < 0 {
		return ""
	}
	return resp[i+4:]
}

// WaitStderrContains polls the server's stderr file until needle shows
// up: a supervisor's log write races the client's observation of the
// reset it logs.
func WaitStderrContains(t *testing.T, path, needle string, within time.Duration) string {
	t.Helper()
	limit := time.Now().Add(within)
	var last string
	for time.Now().Before(limit) {
		last = readFileString(path)
		if strings.Contains(last, needle) {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("stderr never contained %q within %v\n--- stderr ---\n%s", needle, within, last)
	return last
}

func readFileString(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// ThreadedStateServerSource serves with a Map threaded through the
// handler (`serve.run_with`): the count of each path's requests, which
// only threading can carry from one request to the next.
func ThreadedStateServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "core/int";
import "std/platform";

function handle(hits: Map[string, i32], req: HttpRequest, plat: platform.Platform): (Map[string, i32], HttpResponse) {
    let n: i32 = 1;
    match (hits.get(req.path)) {
        Some(prev) => { n = prev + 1; },
        None => {}
    }
    return (hits.insert(req.path, n),
            http.ok(req.path + "=" + int.int_to_string(n)));
}

function main(): i32 {
    let init: Map[string, i32] = map_new(8);
    return serve.run_with(%d, serve.config(), init, handle);
}
`, port)
}

// CheckThreadedState is the client of ThreadedStateServerSource: one
// path's count climbs across requests, a second path starts at one, and
// the first keeps counting from where it was.
func CheckThreadedState(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	for want := 1; want <= 3; want++ {
		if got := ResponseBodyTail(HTTPRoundTrip(t, addr, "/a", 5*time.Second)); got != fmt.Sprintf("/a=%d", want) {
			t.Fatalf("request %d to /a: body %q, want %q (the state did not survive the request boundary)", want, got, fmt.Sprintf("/a=%d", want))
		}
	}
	if got := ResponseBodyTail(HTTPRoundTrip(t, addr, "/b", 5*time.Second)); got != "/b=1" {
		t.Fatalf("first request to /b: body %q, want \"/b=1\"", got)
	}
	if got := ResponseBodyTail(HTTPRoundTrip(t, addr, "/a", 5*time.Second)); got != "/a=4" {
		t.Fatalf("fourth request to /a: body %q, want \"/a=4\"", got)
	}
}

// LargeResponseBytes is the body length of LargeResponseServerSource:
// more than a socket's send buffer takes in one write.
const LargeResponseBytes = 1500000

// LargeResponseServerSource answers every request with LargeResponseBytes
// of body, so the loop has to finish the write on later waits.
func LargeResponseServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/string";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("x".repeat(%d));
}
function main(): i32 {
    return serve.run(%d, serve.config(), handle);
}
`, LargeResponseBytes, port)
}

// CheckLargeResponse reads the whole response twice over: the client
// asks for the close and reads to end of stream, so the loop must also
// hold the close until the deferred write has drained.
func CheckLargeResponse(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	for i := 0; i < 2; i++ {
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		if _, err := conn.Write([]byte("GET /big HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")); err != nil {
			conn.Close()
			t.Fatalf("write: %v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
		got, err := io.ReadAll(conn)
		conn.Close()
		if err != nil {
			t.Fatalf("round %d: read: %v (got %d bytes)", i, err, len(got))
		}
		head, body, ok := strings.Cut(string(got), "\r\n\r\n")
		if !ok {
			t.Fatalf("round %d: no header terminator in %d bytes", i, len(got))
		}
		if !ContainsStatus200(head) {
			t.Fatalf("round %d: not a 200:\n%s", i, head)
		}
		if len(body) != LargeResponseBytes || strings.Trim(body, "x") != "" {
			t.Fatalf("round %d: body is %d bytes, want %d of x", i, len(body), LargeResponseBytes)
		}
	}
}

// RecvDeadlineServerSource serves under a 400 ms per-request read
// deadline.
func RecvDeadlineServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/time";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    return serve.run(%d, serve.Config { ...serve.config(), recv_deadline: time.duration_millis(400) }, handle);
}
`, port)
}

// CheckRecvDeadline sends a partial header and never finishes it: the
// server closes the connection at the deadline without a response, and
// a well-formed request right after still answers 200, so the loop was
// not pinned by the slow client.
func CheckRecvDeadline(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	start := time.Now()
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost:")); err != nil {
		t.Fatalf("partial write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	got, _ := io.ReadAll(conn)
	elapsed := time.Since(start)
	conn.Close()
	if len(got) != 0 {
		t.Fatalf("the slow client got a response to an incomplete request: %q", got)
	}
	if elapsed >= 8*time.Second {
		t.Fatalf("the server never enforced the read deadline (waited %v)", elapsed)
	}
	if resp := HTTPRoundTrip(t, addr, "/ok", 3*time.Second); !ContainsStatus200(resp) {
		t.Fatalf("the request after a timed-out connection did not answer 200:\n%s", resp)
	}
}

// TrappingServerSource is a supervised server with one worker whose
// handler answers 200 on /ok and traps (an array index out of range) on
// /boom.
func TrappingServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "core/int";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/boom") {
        let a: i32[] = [1, 2, 3];
        let i: i32 = a.len() + 5;
        let x: i32 = a[i];
        return http.ok("unreachable " + int.int_to_string(x));
    }
    return http.ok("ok");
}
function main(): i32 {
    return serve.supervise(%d, serve.Config { ...serve.config(), workers: 1 }, handle);
}
`, port)
}

// CheckSurvivesHandlerTrap drives TrappingServerSource: /ok answers, /boom
// gets no 200 and the supervisor logs the worker's death with the raw
// exit code 134, and /ok answers again from the reforked worker.
func CheckSurvivesHandlerTrap(t *testing.T, addr, stderrPath string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("first /ok: want 200, got\n%s", resp)
	}
	if resp := HTTPRoundTrip(t, addr, "/boom", 5*time.Second); strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/boom answered 200:\n%s", resp)
	}
	WaitStderrContains(t, stderrPath, "worker died with exit code 134", 10*time.Second)
	// The refork backoff starts at 100 ms; the parent-owned listener
	// keeps the connection in its backlog meanwhile.
	if resp := HTTPRoundTrip(t, addr, "/ok", 10*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("post-crash /ok: want 200 (the service should have survived), got\n%s", resp)
	}
}

// ReusePortServerSource serves through serve.run with a backlog of
// 4 and SO_REUSEPORT on the listener, in place of tcp_listen's fixed
// 128 and one listener per port.
func ReusePortServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    let opts: serve.Config = serve.Config { ...serve.config(), backlog: 4, reuse_port: true };
    return serve.run(%d, opts, handle);
}
`, port)
}

// soReusePort is SO_REUSEPORT, which Go's syscall package spells only on
// the BSDs: 15 on Linux.
const soReusePort = 15

// CheckReusePortReachesListener drives ReusePortServerSource: the proof
// that the option reached the kernel is a second SO_REUSEPORT socket
// binding the served port while the loop holds it, which a plain
// listener refuses with EADDRINUSE; the loop still answers 200 through
// the first.
func CheckReusePortReachesListener(t *testing.T, port int) {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	WaitServerReady(t, addr, 10*time.Second)
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, soReusePort, 1); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Port: port, Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatalf("a second SO_REUSEPORT socket could not bind the served port, so reuse_port did not reach the listener: %v", err)
	}
	if resp := HTTPRoundTrip(t, addr, "/ok", 3*time.Second); !ContainsStatus200(resp) {
		t.Fatalf("the loop did not answer 200:\n%s", resp)
	}
}

// StalledSurvivorServerSource is WorkersServerSource with /stall holding its
// worker in the handler for a minute, through the bag's clock as /slow does
// (a handler may not reach `sleep_ms` around its bag, E080): the worker the
// crash loop never reaches.
func StalledSurvivorServerSource(port int) string {
	return strings.Replace(WorkersServerSource(port), `    return http.ok("ok");`, `    if (req.path == "/stall") {
        if (burn(plat, 60000000000 as i64) == 0 - 1) { return http.ok("never"); }
    }
    return http.ok("ok");`, 1)
}

// CheckCrashLoopStopsSurvivor drives StalledSurvivorServerSource: one
// worker is held in /stall while the other and its replacements
// crash-loop on /boom; at the give-up the stalled worker is alive and
// holding the listener, and the supervisor stops it (SIGTERM, which a
// handler-held worker cannot act on, then SIGKILL five seconds later)
// before exiting, so the port refuses connections afterwards.
func CheckCrashLoopStopsSurvivor(t *testing.T, cmd *exec.Cmd, addr, stderrPath string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	stalled := rawRequest(t, addr, "/stall", "")
	defer stalled.Close()
	time.Sleep(200 * time.Millisecond)
	CheckCrashLoopGivesUp(t, cmd, addr, stderrPath)
}

// CheckCrashLoopGivesUp drives TrappingServerSource with /boom in a
// reconnect loop: each queued connection is accepted by the next worker
// as soon as it forks and kills it within the 100 ms fast-death window,
// and after eight such deaths the supervisor exits with the child's
// code (134) instead of reforking forever, with no worker left holding
// the port. The doubling backoff waits sum to about 11 s before the
// give-up.
func CheckCrashLoopGivesUp(t *testing.T, cmd *exec.Cmd, addr, stderrPath string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.Now().Add(90 * time.Second)
	var exited bool
	for !exited && time.Now().Before(deadline) {
		select {
		case <-done:
			exited = true
		default:
			conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
			if err != nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			_, _ = conn.Write([]byte("GET /boom HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n"))
			_, _ = io.ReadAll(conn) // the reset, so the requests serialise
			conn.Close()
		}
	}
	if !exited {
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatalf("the supervisor never gave up within the deadline\n--- stderr ---\n%s", readFileString(stderrPath))
		}
	}
	if code := cmd.ProcessState.ExitCode(); code != 134 {
		t.Errorf("supervisor exit = %d, want 134 (the crash-looping child's code)\n--- stderr ---\n%s", code, readFileString(stderrPath))
	}
	stderr := readFileString(stderrPath)
	if !strings.Contains(stderr, "giving up after 8 consecutive fast worker deaths") {
		t.Errorf("the give-up line is missing from stderr:\n%s", stderr)
	}
	if n := strings.Count(stderr, "worker died with exit code 134"); n < 8 {
		t.Errorf("worker-death lines = %d, want at least 8:\n%s", n, stderr)
	}
	// The fast deaths are counted over every worker, so another may have
	// been alive at the give-up: the supervisor stops it before exiting,
	// and nothing is left to accept.
	if conn, err := net.DialTimeout("tcp", addr, 2*time.Second); err == nil {
		conn.Close()
		t.Errorf("the port still accepts after the supervisor gave up: a worker survived it\n--- stderr ---\n%s", stderr)
	}
}

// CheckTrapThenShutdownExitsClean drives TrappingServerSource through a
// trap and a refork, then SIGTERM: the exit is the shutdown's 0, not
// the earlier death's 134.
func CheckTrapThenShutdownExitsClean(t *testing.T, cmd *exec.Cmd, addr, stderrPath string) {
	t.Helper()
	CheckSurvivesHandlerTrap(t, addr, stderrPath)
	sigterm(t, cmd)
	if code := waitExit(t, cmd, 10*time.Second); code != 0 {
		t.Fatalf("supervisor exit code %d after a clean shutdown, want 0: the reforked worker's death leaked into it\n--- stderr ---\n%s", code, readFileString(stderrPath))
	}
}

// MaxConnectionsFloorServerSource serves with `max_connections: 0`,
// which the loop takes as 1: the listener is read whenever no
// connection is held, so requests one at a time are answered.
func MaxConnectionsFloorServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    return serve.run(%d, serve.Config { ...serve.config(), max_connections: 0 }, handle);
}
`, port)
}

// CheckMaxConnectionsFloor drives MaxConnectionsFloorServerSource: three
// requests in turn are each answered 200.
func CheckMaxConnectionsFloor(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	for i := 0; i < 3; i++ {
		if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !ContainsStatus200(resp) {
			t.Fatalf("request %d under max_connections: 0: want 200, got\n%s", i+1, resp)
		}
	}
}

// WorkersServerSource is a supervised server with two workers: /slow
// burns pure work until 1.5 s of the bag's monotonic clock has passed, so
// it holds its worker that long on any runner; /boom traps, /ok answers at
// once.
func WorkersServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
import "core/int";
function burn(plat: platform.Platform, ns: i64): i32 {
    let x: i32 = 12345;
    let until: i64 = plat.elapsed_ns() + ns;
    while (plat.elapsed_ns() < until) {
        let i: i32 = 0;
        while (i < 100000) {
            x = (x * 1103515245 + 12345) & 2147483647;
            i = i + 1;
        }
    }
    return x;
}
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/boom") {
        let a: i32[] = [1, 2, 3];
        let i: i32 = a.len() + 5;
        let x: i32 = a[i];
        return http.ok("unreachable " + int.int_to_string(x));
    }
    if (req.path == "/slow") {
        if (burn(plat, 1500000000 as i64) == 0 - 1) { return http.ok("never"); }
        return http.ok("slow");
    }
    return http.ok("ok");
}
function main(): i32 {
    return serve.supervise(%d, serve.Config { ...serve.config(), workers: 2 }, handle);
}
`, port)
}

// ReusePortWorkersServerSource is TrappingServerSource over two workers
// that each bind their own SO_REUSEPORT listener (`reuse_port`): a
// worker's death takes its listener with it, and its replacement binds
// anew, so CheckSurvivesHandlerTrap proves the service is back on the
// port after a trap.
func ReusePortWorkersServerSource(port int) string {
	return strings.Replace(TrappingServerSource(port), "workers: 1 }", "workers: 2, reuse_port: true }", 1)
}

// CheckWorkersServeSideBySide drives WorkersServerSource: a request on a
// second connection is answered while the first worker is deep in
// /slow, which one worker could not do since its handlers run to
// completion; and a worker's death leaves the other serving, before and
// after the refork.
func CheckWorkersServeSideBySide(t *testing.T, addr, stderrPath string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("first /ok: want 200, got\n%s", resp)
	}
	slow, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	if _, err := io.WriteString(slow, "GET /slow HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	time.Sleep(200 * time.Millisecond)
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/ok beside /slow: want 200, got\n%s", resp)
	}
	if waited := time.Since(started); waited > 500*time.Millisecond {
		t.Fatalf("/ok beside /slow took %v: it waited behind the slow worker rather than being served by the other", waited)
	}
	b, err := io.ReadAll(slow)
	if err != nil || !strings.Contains(string(b), "slow") {
		t.Fatalf("/slow itself: %q, %v", b, err)
	}
	// The proof needs /slow to have outlasted the /ok round trip by far;
	// a burn the machine finishes in a blink would prove nothing.
	if held := time.Since(started); held < 500*time.Millisecond {
		t.Fatalf("/slow held its worker for only %v; too short to show the other worker answered /ok", held)
	}
	if resp := HTTPRoundTrip(t, addr, "/boom", 5*time.Second); strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/boom answered 200:\n%s", resp)
	}
	WaitStderrContains(t, stderrPath, "worker died with exit code 134", 10*time.Second)
	if resp := HTTPRoundTrip(t, addr, "/ok", 10*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("post-crash /ok: want 200 (the other worker should have answered), got\n%s", resp)
	}
}

// BlockingHandlerServerSource is a one-worker server whose /slow handler
// waits on `plat.http` to the fetch upstream's /slow target (through the
// forward proxy SetFetchProxy names, since the upstream is on loopback)
// while /ok answers at once. It is the networking plan's blocking-handler
// conformance case (#9851 §5, #9857).
func BlockingHandlerServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
import "std/fetch";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/slow") {
        match (plat.http(fetch.get("http://8.8.8.8/slow"))) {
            Ok(resp) => { return http.ok("slow " + resp.status.to_string()); },
            Err(e) => { return http.ok("slow err " + e.message()); }
        }
    }
    return http.ok("ok");
}
function main(): i32 {
    return serve.supervise(%d, serve.Config { ...serve.config(), workers: 1 }, handle);
}
`, port)
}

// CheckBlockingHandlerStallsWorker drives BlockingHandlerServerSource and
// asserts what P1 documented: a handler waiting on an upstream holds its
// worker, so /ok on a second connection is answered only once /slow's
// upstream has. P3 (#9857) flips this check to /ok arriving inside the
// upstream's delay.
func CheckBlockingHandlerStallsWorker(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("first /ok: want 200, got\n%s", resp)
	}
	slow, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	started := time.Now()
	if _, err := io.WriteString(slow, "GET /slow HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	// Give the worker time to read /slow and enter its handler before the
	// second connection arrives.
	time.Sleep(20 * time.Millisecond)
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/ok beside /slow: want 200, got\n%s", resp)
	}
	if waited := time.Since(started); waited < SlowUpstreamDelay {
		t.Fatalf("/ok beside /slow took %v, inside the upstream's %v: the worker did not stall on the handler's wait, which this case pins until #9857 lifts it", waited, SlowUpstreamDelay)
	}
	b, err := io.ReadAll(slow)
	if err != nil || !strings.Contains(string(b), "slow 200") {
		t.Fatalf("/slow itself: %q, %v", b, err)
	}
}

// InitStateServerSource is ThreadedStateServerSource with no `main`: an
// `init(): S` and a state-taking `handle` are the two-phase lifecycle the
// compilers synthesise a main for, which serves on `PORT` under the
// supervisor and hands init()'s value to the loop rather than building
// the state per request or dropping it. `init` takes the platform and
// answers the serve config beside the state: one worker, so the count
// every request sees is the one the request before it left.
func InitStateServerSource() string {
	return initStateServerSource(`import "std/serve";`, "serve")
}

// InitStateAliasedServerSource is InitStateServerSource importing std/serve
// as `web`: the config is known by its module, not by the qualifier.
func InitStateAliasedServerSource() string {
	return initStateServerSource(`import "std/serve" as web;`, "web")
}

func initStateServerSource(importLine, qual string) string {
	return strings.NewReplacer("IMPORT", importLine, "QUAL", qual).Replace(`import "std/http";
IMPORT
import "core/int";
import "std/platform";

function init(plat: platform.Platform): (QUAL.Config, Map[string, i32]) {
    return (QUAL.Config { ...QUAL.config(), workers: 1 }, map_new(8));
}

function handle(hits: Map[string, i32], req: HttpRequest, plat: platform.Platform): (Map[string, i32], HttpResponse) {
    let n: i32 = 1;
    match (hits.get(req.path)) {
        Some(prev) => { n = prev + 1; },
        None => {}
    }
    return (hits.insert(req.path, n),
            http.ok(req.path + "=" + int.int_to_string(n)));
}
`)
}

// HandleOnlyServerSource is a handler program with neither `init` nor
// `main`: the synthesised main serves `handle` on `PORT`.
func HandleOnlyServerSource() string {
	return `import "std/http";
import "std/serve";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("path=" + req.path);
}
`
}

// CheckInitState is CheckThreadedState's first half: one path's count
// climbs and a second path starts at one.
func CheckInitState(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	for want := 1; want <= 3; want++ {
		if got := ResponseBodyTail(HTTPRoundTrip(t, addr, "/a", 5*time.Second)); got != fmt.Sprintf("/a=%d", want) {
			t.Fatalf("request %d to /a: body %q, want %q (init's state did not reach the next request)", want, got, fmt.Sprintf("/a=%d", want))
		}
	}
	if got := ResponseBodyTail(HTTPRoundTrip(t, addr, "/b", 5*time.Second)); got != "/b=1" {
		t.Fatalf("first request to /b: body %q, want \"/b=1\"", got)
	}
}

// CheckHandleOnly asks the handle-only server for one path.
func CheckHandleOnly(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	if got := ResponseBodyTail(HTTPRoundTrip(t, addr, "/hello", 5*time.Second)); got != "path=/hello" {
		t.Fatalf("/hello: body %q, want \"path=/hello\"", got)
	}
}

// ResultHandlerServerSource is a handler program whose `handle` answers
// `Result[HttpResponse, http.HttpError]` and fails with `?`: the
// compilers wrap it so the failure is answered as a problem.
func ResultHandlerServerSource() string {
	return `import "std/http";
import "std/serve";
import "std/platform";

function lookup(path: string): Result[string, http.HttpError] {
    if (path == "/items/1") { return Ok("first"); }
    return Err(http.fail(404, "no item at " + path));
}

function handle(req: HttpRequest, plat: platform.Platform): Result[HttpResponse, http.HttpError] {
    let name: string = lookup(req.path)?;
    return Ok(http.ok(name));
}
`
}

// DynErrorHandlerServerSource is a handler program whose `handle` answers
// `Result[HttpResponse, dyn error.Error]`, failing with a concrete error
// that `?` boxes: the compilers adapt it with `respond_error`.
func DynErrorHandlerServerSource() string {
	return dynErrorHandlerSource(`import "std/error";`, "error")
}

// DynErrorHandlerAliasedServerSource is DynErrorHandlerServerSource with
// std/error imported under an alias: the adapter is chosen by the trait's
// identity, not by the spelling the entry gives it.
func DynErrorHandlerAliasedServerSource() string {
	return dynErrorHandlerSource(`import "std/error" as err;`, "err")
}

func dynErrorHandlerSource(imp, q string) string {
	return `import "std/http";
import "std/serve";
import "std/platform";
` + imp + `

struct NotFound { path: string }

impl ` + q + `.Error for NotFound {
    function message(self: Self): string { return "no item at " + self.path; }
}

function lookup(path: string): Result[string, NotFound] {
    if (path == "/items/1") { return Ok("first"); }
    return Err(NotFound { path: path });
}

function handle(req: HttpRequest, plat: platform.Platform): Result[HttpResponse, dyn ` + q + `.Error] {
    let name: string = lookup(req.path)?;
    return Ok(http.ok(name));
}
`
}

// CheckDynErrorHandler drives DynErrorHandlerServerSource: /items/1
// answers 200 with its name, another path a bare 500 problem that does not
// carry the error's message, and the message is on the server's stderr.
func CheckDynErrorHandler(t *testing.T, addr, stderrPath string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	if got := ResponseBodyTail(HTTPRoundTrip(t, addr, "/items/1", 5*time.Second)); got != "first" {
		t.Fatalf("/items/1: body %q, want \"first\"", got)
	}
	resp := HTTPRoundTrip(t, addr, "/items/9", 5*time.Second)
	if !strings.HasPrefix(resp, "HTTP/1.1 500") || !strings.Contains(resp, "application/problem+json") {
		b, _ := os.ReadFile(stderrPath)
		t.Fatalf("/items/9: want a 500 problem, got\n%s\nserver stderr:\n%s", resp, b)
	}
	if got := ResponseBodyTail(resp); got != `{"type":"about:blank","title":"Internal Server Error","status":500}` {
		t.Fatalf("/items/9: body %q, want the bare 500 problem", got)
	}
	const logged = "handler error: no item at /items/9"
	for limit := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		b, _ := os.ReadFile(stderrPath)
		if strings.Contains(string(b), logged) {
			return
		}
		if time.Now().After(limit) {
			t.Fatalf("the server's stderr lacks %q:\n%s", logged, b)
		}
	}
}

// StatefulResultHandlerServerSource threads a count through a handler
// answering `(Map[string, i32], Result[HttpResponse, http.HttpError])`:
// a failure is answered as a problem and the state survives it.
func StatefulResultHandlerServerSource() string {
	return `import "std/http";
import "std/serve";
import "core/int";
import "std/platform";

function init(plat: platform.Platform): (serve.Config, Map[string, i32]) {
    return (serve.Config { ...serve.config(), workers: 1 }, map_new(8));
}

function handle(hits: Map[string, i32], req: HttpRequest, plat: platform.Platform): (Map[string, i32], Result[HttpResponse, http.HttpError]) {
    if (req.path == "/boom") { return (hits, Err(http.fail(404, "nothing here"))); }
    let n: i32 = 1;
    match (hits.get(req.path)) {
        Some(prev) => { n = prev + 1; },
        None => {}
    }
    return (hits.insert(req.path, n), Ok(http.ok(req.path + "=" + int.int_to_string(n))));
}
`
}

// CheckResultHandler drives ResultHandlerServerSource: /items/1 answers
// 200 with its name, and another path the 404 problem the lookup failed
// with.
func CheckResultHandler(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	if got := ResponseBodyTail(HTTPRoundTrip(t, addr, "/items/1", 5*time.Second)); got != "first" {
		t.Fatalf("/items/1: body %q, want \"first\"", got)
	}
	resp := HTTPRoundTrip(t, addr, "/items/9", 5*time.Second)
	if !strings.HasPrefix(resp, "HTTP/1.1 404") || !strings.Contains(resp, "application/problem+json") {
		t.Fatalf("/items/9: want a 404 problem, got\n%s", resp)
	}
	if got := ResponseBodyTail(resp); got != `{"type":"about:blank","title":"Not Found","status":404,"detail":"no item at /items/9"}` {
		t.Fatalf("/items/9: body %q", got)
	}
}

// CheckStatefulResultHandler drives StatefulResultHandlerServerSource:
// the count climbs, /boom answers a 404 problem, and the count climbs on
// from where it was.
func CheckStatefulResultHandler(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	if got := ResponseBodyTail(HTTPRoundTrip(t, addr, "/a", 5*time.Second)); got != "/a=1" {
		t.Fatalf("first /a: body %q, want \"/a=1\"", got)
	}
	if resp := HTTPRoundTrip(t, addr, "/boom", 5*time.Second); !strings.HasPrefix(resp, "HTTP/1.1 404") || !strings.Contains(resp, `"detail":"nothing here"`) {
		t.Fatalf("/boom: want a 404 problem, got\n%s", resp)
	}
	if got := ResponseBodyTail(HTTPRoundTrip(t, addr, "/a", 5*time.Second)); got != "/a=2" {
		t.Fatalf("second /a: body %q, want \"/a=2\" (the state did not survive the failure)", got)
	}
}

// ShutdownHookServerSource threads a request count and declares a
// `shutdown(reason, state)` hook: the compilers wire it to the loop, which
// calls it once it has stopped with the reason and the count as the last
// request left it, and the hook reports both on stderr.
func ShutdownHookServerSource() string {
	return `import "std/http";
import "std/serve";
import "core/int";
import "std/platform";

function init(plat: platform.Platform): (serve.Config, i32) {
    return (serve.Config { ...serve.config(), workers: 1 }, 0);
}

function handle(hits: i32, req: HttpRequest, plat: platform.Platform): (i32, HttpResponse) {
    return (hits + 1, http.ok("hit " + int.int_to_string(hits + 1)));
}

function shutdown(reason: string, hits: i32): void {
    eprint("shutdown reason=" + reason + " hits=" + int.int_to_string(hits));
}
`
}

// CheckShutdownHook drives ShutdownHookServerSource: two requests, then
// SIGTERM; the process exits 0 and its stderr carries the hook's line
// with the reason and the count.
func CheckShutdownHook(t *testing.T, cmd *exec.Cmd, addr, stderrPath string) {
	t.Helper()
	checkShutdownHookOn(t, cmd, addr, stderrPath, syscall.SIGTERM, "sigterm")
}

// CheckShutdownHookOnSigint is CheckShutdownHook with SIGINT, what Ctrl-C
// sends: the supervisor forwards it, the worker drains the same way, and
// the hook's reason names it.
func CheckShutdownHookOnSigint(t *testing.T, cmd *exec.Cmd, addr, stderrPath string) {
	t.Helper()
	checkShutdownHookOn(t, cmd, addr, stderrPath, syscall.SIGINT, "sigint")
}

func checkShutdownHookOn(t *testing.T, cmd *exec.Cmd, addr, stderrPath string, sig syscall.Signal, reason string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	for i := 1; i <= 2; i++ {
		if got := ResponseBodyTail(HTTPRoundTrip(t, addr, "/", 5*time.Second)); got != fmt.Sprintf("hit %d", i) {
			t.Fatalf("request %d: body %q, want \"hit %d\"", i, got, i)
		}
	}
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	if code := waitExit(t, cmd, 10*time.Second); code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if stderr := readFileString(stderrPath); !strings.Contains(stderr, "shutdown reason="+reason+" hits=2") {
		t.Fatalf("the shutdown hook did not report %s on stderr:\n%s", reason, stderr)
	}
}

// StallServerSource is WorkersServerSource with one worker: the shape
// whose handler, running to completion, holds the whole worker.
func StallServerSource(port int) string {
	return strings.Replace(WorkersServerSource(port), "workers: 2", "workers: 1", 1)
}

// CheckHandlerStallsItsWorker drives StallServerSource: a request on a
// second connection is answered only once the first worker's /slow has
// run to completion, since a handler runs on the worker's own thread of
// control and nothing else runs there meanwhile. This pins what P1
// documents so that the phase which changes it inherits a failing test.
func CheckHandlerStallsItsWorker(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	slow, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	if _, err := io.WriteString(slow, "GET /slow HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	type slowResult struct {
		body string
		err  error
		done time.Time
	}
	slowCh := make(chan slowResult, 1)
	go func() {
		b, err := io.ReadAll(slow)
		slowCh <- slowResult{string(b), err, time.Now()}
	}()
	time.Sleep(200 * time.Millisecond)
	if resp := HTTPRoundTrip(t, addr, "/ok", 20*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/ok behind /slow: want 200 once the worker is free, got\n%s", resp)
	}
	okDone := time.Now()
	r := <-slowCh
	if r.err != nil || !strings.Contains(r.body, "slow") {
		t.Fatalf("/slow itself: %q, %v", r.body, r.err)
	}
	// The proof is the order; /slow's 1.5 s deadline keeps it well past the
	// 200 ms head start, and this floor catches a source that loses that.
	if held := r.done.Sub(started); held < 400*time.Millisecond {
		t.Fatalf("/slow held its worker for only %v; too short to show /ok waited behind it", held)
	}
	if okDone.Before(r.done.Add(-50 * time.Millisecond)) {
		t.Fatalf("/ok was answered %v before /slow finished, so the one worker did not run /slow to completion first", r.done.Sub(okDone))
	}
}

// ListenFailureServerSource serves on `port` through the single loop
// (`serve.run`) or the supervisor (`serve.supervise`).
func ListenFailureServerSource(port int, supervised bool) string {
	entry := "run"
	if supervised {
		entry = "supervise"
	}
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    return serve.%s(%d, serve.config(), handle);
}
`, entry, port)
}

// CheckListenFailure runs a server built from ListenFailureServerSource
// on a port something else holds: it exits 98, and stderr names the
// address and the error in words.
func CheckListenFailure(t *testing.T, cmd *exec.Cmd, port int) {
	t.Helper()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("the server did not exit on a port already in use\n--- stderr ---\n%s", stderr.String())
	}
	if code := cmd.ProcessState.ExitCode(); code != 98 {
		t.Errorf("exit code %d, want 98\n--- stderr ---\n%s", code, stderr.String())
	}
	want := fmt.Sprintf("serve: cannot listen on port %d: Address already in use", port)
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
	}
}

// FetchDeadlineUpstreams starts two loopback upstreams for
// FetchDeadlineSource: one that accepts and never replies, and one that
// answers one canned 200 per connection. A loopback listener that cannot
// be had is a failure, not a skip: every networking lane has one.
func FetchDeadlineUpstreams(t *testing.T) (silentPort, livePort int) {
	t.Helper()
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no loopback listener: %v", err)
	}
	t.Cleanup(func() { silent.Close() })
	var held []net.Conn
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			held = append(held, c)
		}
	}()
	live, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no loopback listener: %v", err)
	}
	t.Cleanup(func() { live.Close() })
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
	return silent.Addr().(*net.TCPAddr).Port, live.Addr().(*net.TCPAddr).Port
}

// FetchDeadlineSource is a std/fetch client under its timeouts (#4385):
// against the silent upstream a 400 ms inactivity bound answers
// `Timeout(Inactivity)` and a 300 ms total bound `Timeout(Total)`, each
// checked by the message a caller would print, and against the live one
// the default bounds give a 200 in time. Exit 0 when all three hold.
func FetchDeadlineSource(silentPort, livePort int) string {
	return fmt.Sprintf(`import "std/fetch";
function message_of(answer: Result[HttpResponse, fetch.FetchError]): string {
    match (answer) {
        Ok(resp) => { return "answered"; },
        Err(e) => { return e.message(); }
    }
    return "";
}
function main(): i32 {
    let silent: string = "http://127.0.0.1:%[1]d/";
    let idle: fetch.Timeouts = fetch.Timeouts { connect_ms: 5000, inactivity_ms: 400, total_ms: 5000 };
    if (message_of(fetch.send(fetch.get(silent).with_timeouts(idle))) != "timed out waiting for the response") { return 1; }
    let whole: fetch.Timeouts = fetch.Timeouts { connect_ms: 5000, inactivity_ms: 5000, total_ms: 300 };
    if (message_of(fetch.send(fetch.get(silent).with_timeouts(whole))) != "timed out in all") { return 5; }
    match (fetch.send(fetch.get("http://127.0.0.1:%[2]d/"))) {
        Ok(resp) => {
            if (resp.status == 200) { return 0; }
            return 2;
        },
        Err(e) => { return 3; }
    }
    return 4;
}
`, silentPort, livePort)
}

// CheckFetchDeadline runs a FetchDeadlineSource client: it exits 0, and
// well before the silent upstream could have been waited out.
func CheckFetchDeadline(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("fetch deadline client failed (elapsed %v): %v\n%s", elapsed, err, out)
	}
	if elapsed >= 30*time.Second {
		t.Fatalf("fetch deadline client took %v: the deadline was not enforced", elapsed)
	}
}

// LimitsServerSource serves with the parser's caps lowered through
// `serve.Config.limits`: a 16-byte body and three header fields.
func LimitsServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    let limits: http.HttpLimits = http.HttpLimits { ...http.http_limits(), body: 16, header_fields: 3 };
    return serve.run(%d, serve.Config { ...serve.config(), limits: limits }, handle);
}
`, port)
}

// CheckServeLimits drives LimitsServerSource: a request inside the caps
// is answered 200, a body past 16 bytes 413 and a fourth header field
// 431, each before the handler runs.
func CheckServeLimits(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	ask := func(req string) string {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.WriteString(conn, req); err != nil {
			t.Fatal(err)
		}
		line, _ := bufio.NewReader(conn).ReadString('\n')
		return strings.TrimSpace(line)
	}
	if got := ask("POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 16\r\nConnection: close\r\n\r\n0123456789abcdef"); got != "HTTP/1.1 200 OK" {
		t.Errorf("a 16-byte body: %q, want 200", got)
	}
	if got := ask("POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 17\r\nConnection: close\r\n\r\n0123456789abcdefg"); !strings.HasPrefix(got, "HTTP/1.1 413") {
		t.Errorf("a 17-byte body: %q, want 413", got)
	}
	if got := ask("GET / HTTP/1.1\r\nHost: x\r\nA: 1\r\nB: 2\r\nC: 3\r\n\r\n"); !strings.HasPrefix(got, "HTTP/1.1 431") {
		t.Errorf("four header fields: %q, want 431", got)
	}
}
