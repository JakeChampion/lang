package e2eharness

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
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
// handler (`tcp_serve_with`): the count of each path's requests, which
// only threading can carry from one request to the next.
func ThreadedStateServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/tcp";
import "core/int";

function handle(hits: Map[string, i32], req: HttpRequest, plat: Platform): (Map[string, i32], HttpResponse) {
    var n: i32 = 1;
    match (hits.get(req.path)) {
        Some(prev) => { n = prev + 1; },
        None => {}
    }
    return (hits.insert(req.path, n),
            http.http_response_ok(req.path + "=" + int.int_to_string(n)));
}

function main(): i32 {
    var init: Map[string, i32] = map_new(8);
    return tcp.tcp_serve_with(%d, init, handle);
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
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.http_response_ok("x".repeat(%d));
}
function main(): i32 {
    return tcp.tcp_serve(%d, handle);
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
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.http_response_ok("ok");
}
function main(): i32 {
    return tcp.tcp_serve_deadline(%d, handle, time.duration_millis(400));
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
import "std/tcp";
import "core/int";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    if (req.path == "/boom") {
        var a: i32[] = [1, 2, 3];
        var i: i32 = a.len() + 5;
        var x: i32 = a[i];
        return http.http_response_ok("unreachable " + int.int_to_string(x));
    }
    return http.http_response_ok("ok");
}
function main(): i32 {
    return tcp.tcp_serve_supervised_opts(%d, tcp.ServeOptions { ...tcp.serve_options(), workers: 1 }, handle);
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

// CheckCrashLoopGivesUp drives TrappingServerSource with /boom in a
// reconnect loop: each queued connection is accepted by the next worker
// as soon as it forks and kills it within the 100 ms fast-death window,
// and after eight such deaths the supervisor exits with the child's
// code (134) instead of reforking forever. The doubling backoff sleeps
// sum to about 11 s before the give-up.
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
}

// WorkersServerSource is a supervised server with two workers: /slow
// burns pure work for well over a second, /boom traps, /ok answers at
// once.
func WorkersServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/tcp";
import "core/int";
function burn(n: i32): i32 {
    var x: i32 = 12345;
    var i: i32 = 0;
    while (i < n) {
        x = (x * 1103515245 + 12345) & 2147483647;
        i = i + 1;
    }
    return x;
}
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    if (req.path == "/boom") {
        var a: i32[] = [1, 2, 3];
        var i: i32 = a.len() + 5;
        var x: i32 = a[i];
        return http.http_response_ok("unreachable " + int.int_to_string(x));
    }
    if (req.path == "/slow") {
        if (burn(400000000) == 0 - 1) { return http.http_response_ok("never"); }
        return http.http_response_ok("slow");
    }
    return http.http_response_ok("ok");
}
function main(): i32 {
    return tcp.tcp_serve_supervised_opts(%d, tcp.ServeOptions { ...tcp.serve_options(), workers: 2 }, handle);
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

// InitStateServerSource is ThreadedStateServerSource with no `main`: an
// `init(): S` and a state-taking `handle` are the two-phase lifecycle the
// compilers synthesise a main for, which serves on `PORT` under the
// supervisor and hands init()'s value to the loop rather than building
// the state per request or dropping it. `init` takes the platform and
// answers the serve options beside the state: one worker, so the count
// every request sees is the one the request before it left.
func InitStateServerSource() string {
	return `import "std/http";
import "std/tcp";
import "core/int";

function init(plat: Platform): (tcp.ServeOptions, Map[string, i32]) {
    return (tcp.ServeOptions { ...tcp.serve_options(), workers: 1 }, map_new(8));
}

function handle(hits: Map[string, i32], req: HttpRequest, plat: Platform): (Map[string, i32], HttpResponse) {
    var n: i32 = 1;
    match (hits.get(req.path)) {
        Some(prev) => { n = prev + 1; },
        None => {}
    }
    return (hits.insert(req.path, n),
            http.http_response_ok(req.path + "=" + int.int_to_string(n)));
}
`
}

// HandleOnlyServerSource is a handler program with neither `init` nor
// `main`: the synthesised main serves `handle` on `PORT`.
func HandleOnlyServerSource() string {
	return `import "std/http";
import "std/tcp";

function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.http_response_ok("path=" + req.path);
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
import "std/tcp";

function lookup(path: string): Result[string, http.HttpError] {
    if (path == "/items/1") { return Ok("first"); }
    return Err(http.fail(404, "no item at " + path));
}

function handle(req: HttpRequest, plat: Platform): Result[HttpResponse, http.HttpError] {
    var name: string = lookup(req.path)?;
    return Ok(http.http_response_ok(name));
}
`
}

// StatefulResultHandlerServerSource threads a count through a handler
// answering `(Map[string, i32], Result[HttpResponse, http.HttpError])`:
// a failure is answered as a problem and the state survives it.
func StatefulResultHandlerServerSource() string {
	return `import "std/http";
import "std/tcp";
import "core/int";

function init(plat: Platform): (tcp.ServeOptions, Map[string, i32]) {
    return (tcp.ServeOptions { ...tcp.serve_options(), workers: 1 }, map_new(8));
}

function handle(hits: Map[string, i32], req: HttpRequest, plat: Platform): (Map[string, i32], Result[HttpResponse, http.HttpError]) {
    if (req.path == "/boom") { return (hits, Err(http.fail(404, "nothing here"))); }
    var n: i32 = 1;
    match (hits.get(req.path)) {
        Some(prev) => { n = prev + 1; },
        None => {}
    }
    return (hits.insert(req.path, n), Ok(http.http_response_ok(req.path + "=" + int.int_to_string(n))));
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
import "std/tcp";
import "core/int";

function init(plat: Platform): (tcp.ServeOptions, i32) {
    return (tcp.ServeOptions { ...tcp.serve_options(), workers: 1 }, 0);
}

function handle(hits: i32, req: HttpRequest, plat: Platform): (i32, HttpResponse) {
    return (hits + 1, http.http_response_ok("hit " + int.int_to_string(hits + 1)));
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
	WaitServerReady(t, addr, 10*time.Second)
	for i := 1; i <= 2; i++ {
		if got := ResponseBodyTail(HTTPRoundTrip(t, addr, "/", 5*time.Second)); got != fmt.Sprintf("hit %d", i) {
			t.Fatalf("request %d: body %q, want \"hit %d\"", i, got, i)
		}
	}
	sigterm(t, cmd)
	if code := waitExit(t, cmd, 10*time.Second); code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if stderr := readFileString(stderrPath); !strings.Contains(stderr, "shutdown reason=sigterm hits=2") {
		t.Fatalf("the shutdown hook did not report on stderr:\n%s", stderr)
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
	// /slow is a CPU burn, so its length varies by runner; the proof is the
	// order, and it needs /slow to outlast the 200 ms head start by far.
	if held := r.done.Sub(started); held < 400*time.Millisecond {
		t.Fatalf("/slow held its worker for only %v; too short to show /ok waited behind it", held)
	}
	if okDone.Before(r.done.Add(-50 * time.Millisecond)) {
		t.Fatalf("/ok was answered %v before /slow finished, so the one worker did not run /slow to completion first", r.done.Sub(okDone))
	}
}
