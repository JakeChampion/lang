package e2eharness

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

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
		return ""
	}
	resp, _ := io.ReadAll(conn)
	return string(resp)
}

func ContainsStatus200(resp string) bool {
	return strings.HasPrefix(resp, "HTTP/1.1 200 OK")
}

func ResponseBodyTail(resp string) string {
	i := strings.Index(resp, "\r\n\r\n")
	if i < 0 {
		return ""
	}
	return resp[i+4:]
}

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

func ThreadedStateServerSource() string {
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
    return serve.run_with(0, serve.config(), init, handle);
}
`)
}

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

const LargeResponseBytes = 1500000

func LargeResponseServerSource() string {
	return fmt.Sprintf(`import "std/http";
import "std/string";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("x".repeat(%d));
}
function main(): i32 {
    return serve.run(0, serve.config(), handle);
}
`, LargeResponseBytes)
}

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

func RecvDeadlineServerSource() string {
	return fmt.Sprintf(`import "std/http";
import "std/time";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    return serve.run(0, serve.Config { ...serve.config(), recv_deadline: time.duration_millis(400) }, handle);
}
`)
}

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

func CheckIncompleteRequestAtEOF(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	for _, tc := range []struct {
		name, sent, want string
	}{
		{"head cut short", "GET / HTTP/1.1\r\nHost:", "HTTP/1.1 400"},
		{"body cut short", "POST / HTTP/1.1\r\nHost: h\r\nContent-Length: 10\r\n\r\nabc", "HTTP/1.1 400"},
		{"body expected", "POST / HTTP/1.1\r\nHost: h\r\nExpect: 100-continue\r\nContent-Length: 5\r\n\r\n", "HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 400"},
		{"blank line", "\r\n", ""},
	} {
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			t.Fatalf("%s: dial: %v", tc.name, err)
		}
		if _, err := conn.Write([]byte(tc.sent)); err != nil {
			t.Fatalf("%s: write: %v", tc.name, err)
		}
		if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
			t.Fatalf("%s: half-close: %v", tc.name, err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		got, _ := io.ReadAll(conn)
		conn.Close()
		if tc.want == "" && len(got) != 0 {
			t.Fatalf("%s: want no response, got %q", tc.name, got)
		}
		if tc.want != "" && !strings.HasPrefix(string(got), tc.want) {
			t.Fatalf("%s: want %s, got %q", tc.name, tc.want, got)
		}
	}
	if resp := HTTPRoundTrip(t, addr, "/ok", 3*time.Second); !ContainsStatus200(resp) {
		t.Fatalf("the request after the half-closed connections did not answer 200:\n%s", resp)
	}
}

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

func CheckSurvivesHandlerTrap(t *testing.T, addr, stderrPath string) {
	t.Helper()
	checkSurvivesHandlerTrap(t, addr, stderrPath, false)
}

func CheckOwnListenersSurviveHandlerTrap(t *testing.T, addr, stderrPath string) {
	t.Helper()
	checkSurvivesHandlerTrap(t, addr, stderrPath, true)
}

func checkSurvivesHandlerTrap(t *testing.T, addr, stderrPath string, ownListeners bool) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("first /ok: want 200, got\n%s", resp)
	}
	if resp := HTTPRoundTrip(t, addr, "/boom", 5*time.Second); strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/boom answered 200:\n%s", resp)
	}
	WaitStderrContains(t, stderrPath, "worker died with exit code 134", 10*time.Second)
	if ownListeners {
		WaitServerReady(t, addr, 10*time.Second)
	}
	if resp := HTTPRoundTrip(t, addr, "/ok", 10*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("post-crash /ok: want 200 (the service should have survived), got\n%s", resp)
	}
}

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

var soReusePort = func() int {
	if runtime.GOOS == "darwin" {
		return 0x200
	}
	return 15
}()

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

func DefaultBacklogServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    let opts: serve.Config = serve.Config { ...serve.config(), reuse_port: true, max_connections_per_ip: 0 };
    return serve.run(%d, opts, handle);
}
`, port)
}

const backlogBurst = 512

func CheckDefaultBacklogHoldsBurst(t *testing.T, cmd *exec.Cmd, port int) {
	t.Helper()
	if b, err := os.ReadFile("/proc/sys/net/core/somaxconn"); err == nil {
		var max int
		if _, err := fmt.Sscan(strings.TrimSpace(string(b)), &max); err == nil && max <= backlogBurst {
			t.Skipf("net.core.somaxconn is %d, under the %d-connection burst", max, backlogBurst)
		}
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	WaitServerReady(t, addr, 10*time.Second)
	group := -cmd.Process.Pid
	if err := syscall.Kill(group, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(group, syscall.SIGCONT)
	conns := make([]net.Conn, backlogBurst)
	errs := make([]error, backlogBurst)
	done := make(chan struct{})
	for i := range conns {
		go func(i int) {
			conns[i], errs[i] = net.DialTimeout("tcp", addr, time.Second)
			done <- struct{}{}
		}(i)
	}
	for range conns {
		<-done
	}
	defer func() {
		for _, c := range conns {
			if c != nil {
				c.Close()
			}
		}
	}()
	failed := 0
	var first error
	for _, err := range errs {
		if err != nil {
			if first == nil {
				first = err
			}
			failed++
		}
	}
	if failed > 0 {
		t.Fatalf("%d of %d connections made while the loop accepted none did not complete within a second (first: %v): the accept queue overflowed", failed, backlogBurst, first)
	}
	if err := syscall.Kill(group, syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	for i, c := range conns {
		_ = c.SetDeadline(time.Now().Add(30 * time.Second))
		if _, err := fmt.Fprintf(c, "GET /ok HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
			t.Fatalf("connection %d: %v", i, err)
		}
	}
	for i, c := range conns {
		line, err := bufio.NewReader(c).ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "HTTP/1.1 200") {
			t.Fatalf("connection %d answered %q (%v), not 200", i, line, err)
		}
	}
}

func StalledSurvivorServerSource() string {
	return strings.Replace(WorkersServerSource(), `    return http.ok("ok");`, `    if (req.path == "/stall") {
        if (burn(plat, 60000000000 as i64) == 0 - 1) { return http.ok("never"); }
    }
    return http.ok("ok");`, 1)
}

func CheckCrashLoopStopsSurvivor(t *testing.T, cmd *exec.Cmd, addr, stderrPath string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	stalled := rawRequest(t, addr, "/stall", "")
	defer stalled.Close()
	time.Sleep(200 * time.Millisecond)
	CheckCrashLoopGivesUp(t, cmd, addr, stderrPath)
}

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
			_, _ = io.ReadAll(conn)
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
	if conn, err := net.DialTimeout("tcp", addr, 2*time.Second); err == nil {
		conn.Close()
		t.Errorf("the port still accepts after the supervisor gave up: a worker survived it\n--- stderr ---\n%s", stderr)
	}
}

func CheckTrapThenShutdownExitsClean(t *testing.T, cmd *exec.Cmd, addr, stderrPath string) {
	t.Helper()
	CheckSurvivesHandlerTrap(t, addr, stderrPath)
	sigterm(t, cmd)
	if code := waitExit(t, cmd, 10*time.Second); code != 0 {
		t.Fatalf("supervisor exit code %d after a clean shutdown, want 0: the reforked worker's death leaked into it\n--- stderr ---\n%s", code, readFileString(stderrPath))
	}
}

func MaxConnectionsFloorServerSource() string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    return serve.run(0, serve.Config { ...serve.config(), max_connections: 0 }, handle);
}
`)
}

func CheckMaxConnectionsFloor(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	for i := 0; i < 3; i++ {
		if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !ContainsStatus200(resp) {
			t.Fatalf("request %d under max_connections: 0: want 200, got\n%s", i+1, resp)
		}
	}
}

func WorkersServerSource() string {
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
    return serve.supervise(0, serve.Config { ...serve.config(), workers: 2 }, handle);
}
`)
}

func ReusePortWorkersServerSource(port int) string {
	return strings.Replace(TrappingServerSource(port), "workers: 1 }", "workers: 2, reuse_port: true }", 1)
}

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

func BlockingHandlerServerSource() string {
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
    return serve.supervise(0, serve.Config { ...serve.config(), workers: 1 }, handle);
}
`)
}

func CheckHandlersOverlap(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("first /ok: want 200, got\n%s", resp)
	}
	slow := dialSlow(t, addr)
	defer slow.Close()
	started := time.Now()
	time.Sleep(20 * time.Millisecond)
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/ok beside /slow: want 200, got\n%s", resp)
	}
	if waited := time.Since(started); waited >= SlowUpstreamDelay {
		t.Fatalf("/ok beside /slow took %v, not inside the upstream's %v: the worker stalled on the handler's wait", waited, SlowUpstreamDelay)
	}
	if b, err := io.ReadAll(slow); err != nil || !strings.Contains(string(b), "slow 200") {
		t.Fatalf("/slow itself: %q, %v", b, err)
	}
	first, second := dialSlow(t, addr), dialSlow(t, addr)
	defer first.Close()
	defer second.Close()
	started = time.Now()
	for _, c := range []net.Conn{first, second} {
		if b, err := io.ReadAll(c); err != nil || !strings.Contains(string(b), "slow 200") {
			t.Fatalf("/slow beside /slow: %q, %v", b, err)
		}
	}
	if elapsed := time.Since(started); elapsed >= 2*SlowUpstreamDelay {
		t.Fatalf("two /slow on two connections took %v, not inside the upstream's %v doubled: the handlers ran in turn", elapsed, SlowUpstreamDelay)
	}
	piped, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer piped.Close()
	if _, err := io.WriteString(piped, "GET /slow HTTP/1.1\r\nHost: localhost\r\n\r\nGET /ok HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	all, err := io.ReadAll(piped)
	if err != nil {
		t.Fatal(err)
	}
	at := strings.Index(string(all), "slow 200")
	if at < 0 || !strings.Contains(string(all[at:]), "\r\n\r\nok") {
		t.Fatalf("pipelined /slow then /ok: want the slow answer first, got\n%s", all)
	}
	cut, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cut.Close()
	if _, err := io.WriteString(cut, "GET /slow HTTP/1.1\r\nHost: localhost\r\n\r\nGET /ok HTTP/1.1\r\nHo"); err != nil {
		t.Fatal(err)
	}
	if err := cut.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	cutAll, err := io.ReadAll(cut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(cutAll), "HTTP/1.1 200") || !strings.Contains(string(cutAll), "slow 200") || strings.Contains(string(cutAll), " 400 ") {
		t.Fatalf("parked /slow then a cut-short request and the end of stream: want only the slow answer, got\n%s", cutAll)
	}
	gone := dialSlow(t, addr)
	gone.Close()
	time.Sleep(20 * time.Millisecond)
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/ok after a client left mid-/slow: want 200, got\n%s", resp)
	}
	time.Sleep(2 * SlowUpstreamDelay)
	if resp := HTTPRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/ok after the abandoned /slow answered: want 200, got\n%s", resp)
	}
}

func dialSlow(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(c, "GET /slow HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	return c
}

func InitStateServerSource() string {
	return initStateServerSource(`import "std/serve";`, "serve")
}

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

func HandleOnlyServerSource() string {
	return `import "std/http";
import "std/serve";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("path=" + req.path);
}
`
}

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

func CheckHandleOnly(t *testing.T, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	if got := ResponseBodyTail(HTTPRoundTrip(t, addr, "/hello", 5*time.Second)); got != "path=/hello" {
		t.Fatalf("/hello: body %q, want \"path=/hello\"", got)
	}
}

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

func DynErrorHandlerServerSource() string {
	return dynErrorHandlerSource(`import "std/error";`, "error")
}

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

func CheckShutdownHook(t *testing.T, cmd *exec.Cmd, addr, stderrPath string) {
	t.Helper()
	checkShutdownHookOn(t, cmd, addr, stderrPath, syscall.SIGTERM, "sigterm")
}

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

func StallServerSource() string {
	return strings.Replace(WorkersServerSource(), "workers: 2", "workers: 1", 1)
}

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
	if held := r.done.Sub(started); held < 400*time.Millisecond {
		t.Fatalf("/slow held its worker for only %v; too short to show /ok waited behind it", held)
	}
	if okDone.Before(r.done.Add(-50 * time.Millisecond)) {
		t.Fatalf("/ok was answered %v before /slow finished, so the one worker did not run /slow to completion first", r.done.Sub(okDone))
	}
}

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
	want := regexp.MustCompile(fmt.Sprintf(`serve: cannot listen on (\[::\]|0\.0\.0\.0):%d: Address already in use`, port))
	if !want.MatchString(stderr.String()) {
		t.Errorf("stderr does not match %q:\n%s", want, stderr.String())
	}
}

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

func FetchDeadlineSource(silentPort, livePort int) string {
	return fmt.Sprintf(`import "std/fetch";
import "std/time";
function message_of(answer: Result[HttpResponse, fetch.FetchError]): string {
    match (answer) {
        Ok(resp) => { return "answered"; },
        Err(e) => { return e.message(); }
    }
    return "";
}
function main(): i32 {
    let silent: string = "http://127.0.0.1:%[1]d/";
    let idle: fetch.Timeouts = fetch.Timeouts { connect: time.duration_seconds(5 as i64), inactivity: time.duration_millis(400 as i64), total: time.duration_seconds(5 as i64) };
    if (message_of(fetch.send(fetch.get(silent).with_timeouts(idle))) != "timed out waiting for the response") { return 1; }
    let whole: fetch.Timeouts = fetch.Timeouts { connect: time.duration_seconds(5 as i64), inactivity: time.duration_seconds(5 as i64), total: time.duration_millis(300 as i64) };
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

func LimitsServerSource() string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    let limits: http.HttpLimits = http.HttpLimits { ...http.http_limits(), body: 16, header_fields: 3 };
    return serve.run(0, serve.Config { ...serve.config(), limits: limits }, handle);
}
`)
}

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
