package e2eharness

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

// Graceful shutdown (#9854, the seven steps of #9851 §2), shared by the
// native tests and their self-host twins: SIGTERM keeps the loop accepting
// for a grace, fails the readiness path, closes the listener, ends
// keep-alive (a request whose keep-alive was decided before the signal is
// answered and then closed), drains what is in flight under a deadline and
// exits 0, or 1 when it cut a request off; the supervisor forwards the
// signal to its workers and waits for them; and a listener handed in
// through LISTEN_FDS is served instead of a fresh one.

// ServeShutdownSource is a server on `port` whose /slow burns for well
// over a second, whose readiness path is /healthz and which answers
// everything else at once, with a 300 ms grace and `drainMs` to drain.
// `entry` is the serve call, a format with one %d for the port that reads
// `opts`.
func ServeShutdownSource(port, drainMs int, entry string) string {
	return fmt.Sprintf(`import "std/http";
import "std/tcp";
import "std/time";
function burn(n: i32): i32 {
    let x: i32 = 12345;
    let i: i32 = 0;
    while (i < n) {
        x = (x * 1103515245 + 12345) & 2147483647;
        i = i + 1;
    }
    return x;
}
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    if (req.path == "/slow") {
        if (burn(400000000) == 0 - 1) { return http.ok("never"); }
        return http.ok("slow");
    }
    return http.ok("ok");
}
function main(): i32 {
    let opts: tcp.ServeOptions = tcp.ServeOptions { ...tcp.serve_options(), shutdown_grace: time.duration_millis(300 as i64), readiness_path: "/healthz", drain_deadline: time.duration_millis(%d as i64) };
    return %s;
}
`, drainMs, fmt.Sprintf(entry, port))
}

// StartServerProcess starts cmd in its own process group, with its stderr
// in a file the caller can read, and kills the whole group at cleanup: a
// supervisor forks workers, and killing only the parent would orphan a
// worker still holding the listener. extraFiles are handed to the child
// from descriptor 3 up, the way LISTEN_FDS announces a listener.
func StartServerProcess(t *testing.T, cmd *exec.Cmd, extraFiles ...*os.File) (stderrPath string) {
	t.Helper()
	cmd.ExtraFiles = append(cmd.ExtraFiles, extraFiles...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderrPath = filepath.Join(t.TempDir(), "stderr.log")
	errFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatalf("create stderr file: %v", err)
	}
	cmd.Stderr = errFile
	if err := cmd.Start(); err != nil {
		errFile.Close()
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
		errFile.Close()
	})
	return stderrPath
}

// WaitServerReady dials until the listener accepts. The probe connection
// closes without sending a request, which the loop reads as a closed
// connection and moves on from.
func WaitServerReady(t *testing.T, addr string, within time.Duration) {
	t.Helper()
	limit := time.Now().Add(within)
	for time.Now().Before(limit) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server never bound on %s within %v", addr, within)
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

func expectEOF(t *testing.T, conn net.Conn, label string) {
	t.Helper()
	if b, err := io.ReadAll(conn); err != nil || len(b) != 0 {
		t.Fatalf("%s: %q, %v; want EOF", label, b, err)
	}
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
		t.Fatalf("the server did not exit within %v of SIGTERM; its children: %s", within, childrenState(cmd.Process.Pid))
	}
	return -1
}

// childrenState names each child the process still has, with the kernel
// function it waits in, for the report of a shutdown that hung.
func childrenState(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	if err != nil {
		return err.Error()
	}
	var out []string
	for _, child := range strings.Fields(string(b)) {
		wchan, _ := os.ReadFile("/proc/" + child + "/wchan")
		status, _ := os.ReadFile("/proc/" + child + "/status")
		state := ""
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "State:") {
				state = strings.TrimSpace(strings.TrimPrefix(line, "State:"))
			}
		}
		out = append(out, fmt.Sprintf("%s (%s, in %s)", child, state, strings.TrimSpace(string(wchan))))
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ", ")
}

func sigterm(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
}

// CheckShutdownDrains drives a server built from ServeShutdownSource with
// a drain deadline longer than /slow: an idle keep-alive connection and a
// request in flight are open when SIGTERM arrives; the readiness path
// answers 503 with close within the grace, the in-flight request is
// answered and its connection closed, the idle one is closed, and the
// process exits 0.
func CheckShutdownDrains(t *testing.T, cmd *exec.Cmd, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	idle := rawRequest(t, addr, "/ok", "")
	defer idle.Close()
	if resp := readResponse(t, idle, "idle /ok"); resp.StatusCode != 200 || resp.Close {
		t.Fatalf("idle /ok: status %d close=%v, want 200 kept", resp.StatusCode, resp.Close)
	}
	slow := rawRequest(t, addr, "/slow", "")
	defer slow.Close()
	time.Sleep(100 * time.Millisecond)
	sigterm(t, cmd)
	// Within the grace the loop still accepts; the readiness path says
	// not ready, with close, once the slow handler is done.
	probe := rawRequest(t, addr, "/healthz", "")
	defer probe.Close()
	if resp := readResponse(t, probe, "/healthz after SIGTERM"); resp.StatusCode != 503 || !resp.Close {
		t.Fatalf("/healthz after SIGTERM: status %d close=%v, want 503 close", resp.StatusCode, resp.Close)
	}
	// The in-flight request's keep-alive was decided before the signal,
	// so the close follows the response instead of being announced.
	resp := readResponse(t, slow, "/slow across SIGTERM")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "slow" {
		t.Fatalf("/slow across SIGTERM: status %d body %q, want 200 slow", resp.StatusCode, body)
	}
	expectEOF(t, slow, "the answered connection after shutdown")
	expectEOF(t, idle, "the idle connection after shutdown")
	if code := waitExit(t, cmd, 10*time.Second); code != 0 {
		t.Fatalf("exit code %d, want 0 after a clean drain", code)
	}
}

// CheckShutdownAbortsAtDrainDeadline drives a server built with a 400 ms
// drain deadline: a request that never completes is cut off at the
// deadline and the process exits 1.
func CheckShutdownAbortsAtDrainDeadline(t *testing.T, cmd *exec.Cmd, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
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
	sigterm(t, cmd)
	if err := partial.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	expectEOF(t, partial, "the partial request's connection at the drain deadline")
	if waited := time.Since(started); waited > 5*time.Second {
		t.Fatalf("the partial request was cut off after %v, not at the 400 ms drain deadline", waited)
	}
	if code := waitExit(t, cmd, 10*time.Second); code != 1 {
		t.Fatalf("exit code %d, want 1 after cutting a request off", code)
	}
}

// CheckSupervisedShutdown drives a supervisor over two workers: SIGTERM to
// the supervisor is forwarded, a request in flight on a worker is
// answered, the supervisor exits 0 once its workers drained, and no
// worker's shutdown is logged as a death.
func CheckSupervisedShutdown(t *testing.T, cmd *exec.Cmd, addr, stderrPath string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	slow := rawRequest(t, addr, "/slow", "")
	defer slow.Close()
	time.Sleep(100 * time.Millisecond)
	sigterm(t, cmd)
	resp := readResponse(t, slow, "/slow across a forwarded SIGTERM")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "slow" {
		t.Fatalf("/slow across a forwarded SIGTERM: status %d body %q, want 200 slow", resp.StatusCode, body)
	}
	expectEOF(t, slow, "the answered connection after the workers drained")
	if code := waitExit(t, cmd, 10*time.Second); code != 0 {
		t.Fatalf("supervisor exit code %d, want 0 after its workers drained", code)
	}
	if b, _ := os.ReadFile(stderrPath); strings.Contains(string(b), "worker died") {
		t.Fatalf("a worker's shutdown was logged as a death:\n%s", b)
	}
}

// InheritedListener is a listening socket and its file, for
// StartServerProcess to hand to a server as descriptor 3.
func InheritedListener(t *testing.T) (addr string, file *os.File) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	file, err = ln.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return ln.Addr().String(), file
}

// CheckInheritedListener drives a server started with an inherited
// listener and LISTEN_FDS=1: it answers on that listener, and exits 0 on
// SIGTERM.
func CheckInheritedListener(t *testing.T, cmd *exec.Cmd, addr string) {
	t.Helper()
	conn := rawRequest(t, addr, "/ok", "Connection: close\r\n")
	defer conn.Close()
	if resp := readResponse(t, conn, "/ok on the inherited listener"); resp.StatusCode != 200 {
		t.Fatalf("/ok on the inherited listener: status %d, want 200", resp.StatusCode)
	}
	sigterm(t, cmd)
	if code := waitExit(t, cmd, 10*time.Second); code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
}
