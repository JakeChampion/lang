package e2e

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Graceful shutdown (#9854, the seven steps of #9851 §2), on the native
// x86-64 backend: SIGTERM keeps the loop accepting for a grace, fails the
// readiness path, closes the listener, ends keep-alive (a request whose
// keep-alive was decided before the signal is answered and then closed),
// drains what is in flight under a deadline and exits 0, or 1 when it cut
// a request off;
// the supervisor forwards the signal to its workers and waits for them;
// and a listener handed in through LISTEN_FDS is served instead of a
// fresh one.

// shutdownServeSrc: /slow burns for well over a second, /healthz is the
// readiness path, everything else answers at once. The grace is 300 ms
// and the drain deadline 5 s; the serve entry point is the format's.
const shutdownServeSrc = `
import "std/http";
import "std/tcp";
import "std/time";
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
    if (req.path == "/slow") {
        if (burn(400000000) == 0 - 1) { return http.http_response_ok("never"); }
        return http.http_response_ok("slow");
    }
    return http.http_response_ok("ok");
}
function main(): i32 {
    var opts: tcp.ServeOptions = tcp.ServeOptions { ...tcp.serve_options(), shutdown_grace: time.duration_millis(300 as i64), readiness_path: "/healthz", drain_deadline: time.duration_millis(%d as i64) };
    return %s;
}`

func shutdownSrc(port int, drainMs int, entry string) string {
	return fmt.Sprintf(shutdownServeSrc, drainMs, fmt.Sprintf(entry, port))
}

// rawRequest writes one request on a fresh connection and returns the
// connection, for the caller to read at its own pace.
func rawRequest(t *testing.T, addr, path, connection string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: localhost\r\n"+connection+"\r\n"); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	return conn
}

func readResponse(t *testing.T, conn net.Conn, label string) *http.Response {
	t.Helper()
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("%s: body: %v", label, err)
	}
	resp.Body = io.NopCloser(strings.NewReader(string(body)))
	return resp
}

func waitExit(t *testing.T, cmd *exec.Cmd, within time.Duration) int {
	t.Helper()
	done := make(chan int, 1)
	go func() {
		_ = cmd.Wait()
		done <- cmd.ProcessState.ExitCode()
	}()
	select {
	case code := <-done:
		return code
	case <-time.After(within):
		t.Fatalf("the server did not exit within %v of SIGTERM", within)
	}
	return -1
}

func TestServeShutdownDrainsAndExitsClean(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, shutdownSrc(port, 5000, "tcp.tcp_serve_opts(%d, opts, handle)"))
	cmd, _ := startSupervisedServer(t, bin, runner)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	waitServerReady(t, addr, 10*time.Second)

	// An idle keep-alive connection, answered once and left open.
	idle := rawRequest(t, addr, "/ok", "")
	defer idle.Close()
	if resp := readResponse(t, idle, "idle /ok"); resp.StatusCode != 200 || resp.Close {
		t.Fatalf("idle /ok: status %d close=%v, want 200 kept", resp.StatusCode, resp.Close)
	}
	// A request in flight: /slow holds the loop for well over a second.
	slow := rawRequest(t, addr, "/slow", "")
	defer slow.Close()
	time.Sleep(100 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	// Within the grace the loop still accepts; the readiness path says
	// not ready, with close, once the slow handler is done.
	probe := rawRequest(t, addr, "/healthz", "")
	defer probe.Close()
	if resp := readResponse(t, probe, "/healthz after SIGTERM"); resp.StatusCode != 503 || !resp.Close {
		t.Fatalf("/healthz after SIGTERM: status %d close=%v, want 503 close", resp.StatusCode, resp.Close)
	}
	// The in-flight request is answered (its keep-alive was decided
	// before the signal, so the close follows the response instead).
	resp := readResponse(t, slow, "/slow across SIGTERM")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "slow" {
		t.Fatalf("/slow across SIGTERM: status %d body %q, want 200 slow", resp.StatusCode, body)
	}
	if b, err := io.ReadAll(slow); err != nil || len(b) != 0 {
		t.Fatalf("the answered connection after shutdown: %q, %v; want EOF", b, err)
	}
	// The idle connection is closed by the server, and the process exits 0.
	if b, err := io.ReadAll(idle); err != nil || len(b) != 0 {
		t.Fatalf("idle connection after shutdown: %q, %v; want EOF", b, err)
	}
	if code := waitExit(t, cmd, 10*time.Second); code != 0 {
		t.Fatalf("exit code %d, want 0 after a clean drain", code)
	}
}

func TestServeShutdownAbortsAtDrainDeadline(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, shutdownSrc(port, 400, "tcp.tcp_serve_opts(%d, opts, handle)"))
	cmd, _ := startSupervisedServer(t, bin, runner)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	waitServerReady(t, addr, 10*time.Second)

	// A request that never completes holds its connection in flight.
	partial, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer partial.Close()
	if _, err := io.WriteString(partial, "GET /never HTTP/1.1\r\nHost: localhost\r\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	started := time.Now()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := partial.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if b, err := io.ReadAll(partial); err != nil || len(b) != 0 {
		t.Fatalf("the partial request's connection: %q, %v; want EOF at the drain deadline", b, err)
	}
	if waited := time.Since(started); waited > 5*time.Second {
		t.Fatalf("the partial request was cut off after %v, not at the 400 ms drain deadline", waited)
	}
	if code := waitExit(t, cmd, 10*time.Second); code != 1 {
		t.Fatalf("exit code %d, want 1 after cutting a request off", code)
	}
}

func TestSupervisedServeForwardsShutdown(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, shutdownSrc(port, 5000, "tcp.tcp_serve_supervised_opts(%d, tcp.ServeOptions { ...opts, workers: 2 }, handle)"))
	cmd, stderrPath := startSupervisedServer(t, bin, runner)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	waitServerReady(t, addr, 10*time.Second)

	slow := rawRequest(t, addr, "/slow", "")
	defer slow.Close()
	time.Sleep(100 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	resp := readResponse(t, slow, "/slow across a forwarded SIGTERM")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "slow" {
		t.Fatalf("/slow across a forwarded SIGTERM: status %d body %q, want 200 slow", resp.StatusCode, body)
	}
	if b, err := io.ReadAll(slow); err != nil || len(b) != 0 {
		t.Fatalf("the answered connection after the workers drained: %q, %v; want EOF", b, err)
	}
	if code := waitExit(t, cmd, 10*time.Second); code != 0 {
		t.Fatalf("supervisor exit code %d, want 0 after its workers drained", code)
	}
	if b, _ := os.ReadFile(stderrPath); strings.Contains(string(b), "worker died") {
		t.Fatalf("a worker's shutdown was logged as a death:\n%s", b)
	}
}

// startSupervisedServerWithListener is startSupervisedServer with a
// listening socket handed to the server as descriptor 3, the way systemd
// passes one, and the environment entries that announce it.
func startSupervisedServerWithListener(t *testing.T, bin string, runner []string, listener *os.File, extraEnv ...string) (cmd *exec.Cmd, stderrPath string) {
	t.Helper()
	var c *exec.Cmd
	if len(runner) == 0 {
		c = exec.Command(bin)
	} else {
		c = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	c.Env = append(os.Environ(), extraEnv...)
	c.ExtraFiles = []*os.File{listener}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderrPath = filepath.Join(t.TempDir(), "stderr.log")
	errFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatalf("create stderr file: %v", err)
	}
	c.Stderr = errFile
	if err := c.Start(); err != nil {
		errFile.Close()
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		_, _ = c.Process.Wait()
		errFile.Close()
	})
	return c, stderrPath
}

func TestServeInheritsListenFds(t *testing.T) {
	bin, runner := buildSupervisedServeBin(t, shutdownSrc(0, 5000, "tcp.tcp_serve_opts(%d, opts, handle)"))
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	file, err := ln.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	cmd, _ := startSupervisedServerWithListener(t, bin, runner, file, "LISTEN_FDS=1")
	addr := ln.Addr().String()
	if resp := httpRoundTrip(t, addr, "/ok", 5*time.Second); !strings.Contains(resp, "HTTP/1.1 200") {
		t.Fatalf("/ok on the inherited listener: want 200, got\n%s", resp)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code := waitExit(t, cmd, 10*time.Second); code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
}
