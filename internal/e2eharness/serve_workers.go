package e2eharness

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
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

// OrphanedWorkersServerSource is a supervised server on `port` with two
// workers.
func OrphanedWorkersServerSource(port int) string {
	return fmt.Sprintf(`import "std/http";
import "std/tcp";
import "std/time";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.ok("ok");
}
function main(): i32 {
    return tcp.tcp_serve_supervised_opts(%d, tcp.ServeOptions { ...tcp.serve_options(), workers: 2, shutdown_grace: time.duration_millis(100 as i64) }, handle);
}
`, port)
}

// CheckWorkersStopWithSupervisor SIGKILLs the supervisor at cmd while an
// idle keep-alive connection is open: the supervisor forwards nothing,
// so each worker must see its parent's exit itself and shut down. The
// idle connection is closed, both workers exit, and with them the last
// holder of the listener, so the port refuses connections.
func CheckWorkersStopWithSupervisor(t *testing.T, cmd *exec.Cmd, addr string) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	pid := cmd.Process.Pid
	path := fmt.Sprintf("/proc/%d/task/%d/children", pid, pid)
	var workers []string
	for limit := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		workers = strings.Fields(string(b))
		if len(workers) == 2 || time.Now().After(limit) {
			break
		}
	}
	if len(workers) != 2 {
		t.Fatalf("the supervisor has %d workers, want 2", len(workers))
	}
	idle := rawRequest(t, addr, "/ok", "")
	defer idle.Close()
	if resp := readResponse(t, idle, "idle /ok"); resp.StatusCode != 200 || resp.Close {
		t.Fatalf("idle /ok: status %d close=%v, want 200 kept", resp.StatusCode, resp.Close)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = cmd.Process.Wait()
	_ = idle.SetReadDeadline(time.Now().Add(10 * time.Second))
	if n, err := idle.Read(make([]byte, 1)); err == nil || os.IsTimeout(err) {
		t.Fatalf("the idle connection after the supervisor died: %d bytes, %v; want it closed", n, err)
	}
	for _, w := range workers {
		stat := fmt.Sprintf("/proc/%s/stat", w)
		for limit := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			b, err := os.ReadFile(stat)
			if err != nil || strings.Contains(string(b), ") Z ") {
				break
			}
			if time.Now().After(limit) {
				t.Fatalf("worker %s still runs 10 s after its supervisor died: %s", w, b)
			}
		}
	}
	if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		conn.Close()
		t.Fatal("the port still accepts once every worker has exited")
	}
}

// ParentGoneProbe forks: the parent prints the child's pid and exits, and
// the child, once its parent has long gone, prints what `watch_parent`
// answers and exits.
func ParentGoneProbe() string {
	return `import "std/async";
import "core/int";
function main(): i32 {
    var pid: i32 = proc_fork();
    if (pid != 0) {
        print(int.int_to_string(pid));
        return 0;
    }
    sleep_ms(500 as i64);
    var drv: async.RealDriver = async.real_driver();
    print(int.int_to_string(drv.watch_parent()));
    return 0;
}
`
}

// CheckParentGone drives a started ParentGoneProbe at cmd whose stdout is
// `out`: once its parent has exited, the child's watch_parent answers
// -ESRCH (-3) when the kernel reparented it to init, and 0 when a
// subreaper took it, which hides the parent's death from it.
func CheckParentGone(t *testing.T, cmd *exec.Cmd, out io.Reader) {
	t.Helper()
	lines := bufio.NewScanner(out)
	if !lines.Scan() {
		t.Fatal("the probe printed no child pid")
	}
	child := strings.TrimSpace(lines.Text())
	var ppid string
	for limit := time.Now().Add(10 * time.Second); ppid == "" || ppid == strconv.Itoa(cmd.Process.Pid); time.Sleep(20 * time.Millisecond) {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%s/stat", child))
		if err != nil {
			t.Fatalf("read the child's stat: %v", err)
		}
		fields := strings.Fields(string(b[strings.LastIndexByte(string(b), ')')+1:]))
		ppid = fields[1]
		if time.Now().After(limit) {
			t.Fatalf("the child's parent is still %s", ppid)
		}
	}
	t.Logf("the child was reparented to %s", ppid)
	want := "0"
	if ppid == "1" {
		want = "-3"
	}
	if !lines.Scan() {
		t.Fatal("the child printed no watch_parent result")
	}
	if got := strings.TrimSpace(lines.Text()); got != want {
		t.Fatalf("watch_parent after the parent exited, reparented to %s: %s, want %s", ppid, got, want)
	}
}
