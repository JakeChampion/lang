package e2eharness

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// WorkersPerCPUServerSource is a supervised server on `port` with the
// default options, whose worker count is one per processing unit.
func WorkersPerCPUServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    return tcp.tcp_serve_supervised(%d, handle);
}
`, port)
}

// CheckWorkersPerCPU waits for the supervisor at cmd to have forked one
// worker per processing unit the test process may use (the affinity
// mask Fern's `cpu_count()` and Go's runtime.NumCPU both count), read
// from the supervisor's children under /proc, and for the service to
// answer.
func CheckWorkersPerCPU(t *testing.T, cmd *exec.Cmd, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	want := runtime.NumCPU()
	pid := cmd.Process.Pid
	path := fmt.Sprintf("/proc/%d/task/%d/children", pid, pid)
	limit := time.Now().Add(10 * time.Second)
	var got int
	for {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		got = len(strings.Fields(string(b)))
		if got == want || time.Now().After(limit) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got != want {
		t.Fatalf("the supervisor has %d workers, want one per processing unit (%d)", got, want)
	}
	conn := rawRequest(t, addr, "/ok", "Connection: close\r\n")
	defer conn.Close()
	if resp := readResponse(t, conn, "/ok with a worker per processing unit"); resp.StatusCode != 200 {
		t.Fatalf("/ok: status %d, want 200", resp.StatusCode)
	}
}

// BurstServerSource is a supervised server on `port` with four workers
// and a 100 ms accept grace, answering everything at once.
func BurstServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/tcp";
import "std/time";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    return tcp.tcp_serve_supervised_opts(%d, tcp.ServeOptions { ...tcp.serve_options(), workers: 4, shutdown_grace: time.duration_millis(100 as i64) }, handle);
}
`, port)
}

// CheckShutdownAfterBurst answers 64 connections in a row over the
// four workers and then sends SIGTERM: every worker must exit. A wake-up
// over the shared listener can reach a worker that another beat to the
// connection, and one held in a blocking accept from then on would
// never see the signal.
func CheckShutdownAfterBurst(t *testing.T, cmd *exec.Cmd, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	for i := 0; i < 64; i++ {
		conn := rawRequest(t, addr, "/ok", "Connection: close\r\n")
		resp := readResponse(t, conn, fmt.Sprintf("/ok, connection %d", i))
		conn.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("/ok, connection %d: status %d, want 200", i, resp.StatusCode)
		}
	}
	sigterm(t, cmd)
	if code := waitExit(t, cmd, 10*time.Second); code != 0 {
		t.Fatalf("supervisor exit code %d, want 0 once every worker drained", code)
	}
}
