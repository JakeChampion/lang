package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The reactor-unification payoff (docs/ASYNC-IMPLEMENTATION-PLAN.md
// Phase 1/4 option 1): std/task — the PORTABLE reactor that the
// `concurrent` / `race` / `await` desugar emits onto — now drives REAL
// overlapping I/O on the native backends, not just simulated in-memory
// completions. A task suspended via `register_fd(fd)` / `Wait(tok, …)`
// is resumed when its fd is readable, because `poll_ready()` blocks in
// the real `poll` syscall once the in-memory queue is empty.
//
// This is the same two-parallel-fetches fan-out as
// TestReactorOutboundFanout, but through std/task's Step / Reactor /
// run API (the one user `concurrent { … }` blocks compile to) rather
// than std/reactor's native-only IoStep — proving the unified reactor
// overlaps two real sockets on one thread. A Go upstream answers both
// connections; exit 42 iff both tasks completed with a response.
func TestTaskReactorRealFdFanout(t *testing.T) {
	bin := buildFernCLI(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				b := make([]byte, 256)
				_, _ = c.Read(b)
				fmt.Fprint(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
			}(conn)
		}
	}()

	// 127.0.0.1 in network byte order, packed: 127 | (1 << 24).
	const host = 127 | (1 << 24)

	// await-all: run() drives both real sockets to completion.
	runSrc := fmt.Sprintf(`import "std/task";

function start_fetch(rx: task.Reactor, conn: i32): (task.Step, task.Reactor) {
    var (tok, rx2) = rx.register_fd(conn);
    function resume(woken_fd: i32, r: task.Reactor): (task.Step, task.Reactor) {
        var resp: string = tcp_recv(woken_fd, 4096);
        if (resp.len() > 0) { return (Done(1), r); }
        return (Done(0), r);
    }
    return (Wait(tok, resume), rx2);
}

function main(): i32 {
    var c1: i32 = tcp_connect(%d, %d);
    var c2: i32 = tcp_connect(%d, %d);
    if (c1 < 0) { return 81; }
    if (c2 < 0) { return 82; }
    if (tcp_send(c1, "GET /1 HTTP/1.1\r\nHost: x\r\n\r\n") < 0) { return 83; }
    if (tcp_send(c2, "GET /2 HTTP/1.1\r\nHost: x\r\n\r\n") < 0) { return 84; }
    var rx0: task.Reactor = task.reactor_new();
    var (s1, rx1) = start_fetch(rx0, c1);
    var (s2, rx2) = start_fetch(rx1, c2);
    var states: task.Step[] = [s1, s2];
    var results: i32[] = task.run(states, rx2);
    if (results[0] == 1 && results[1] == 1) { return 42; }
    return 85;
}`, host, port, host, port)

	// race: select() drives both, returns the FIRST to complete. Both
	// upstreams answer 1, so the winner's result is deterministically 1
	// regardless of which socket the OS reports ready first.
	selectSrc := fmt.Sprintf(`import "std/task";

function start_fetch(rx: task.Reactor, conn: i32): (task.Step, task.Reactor) {
    var (tok, rx2) = rx.register_fd(conn);
    function resume(woken_fd: i32, r: task.Reactor): (task.Step, task.Reactor) {
        var resp: string = tcp_recv(woken_fd, 4096);
        if (resp.len() > 0) { return (Done(1), r); }
        return (Done(0), r);
    }
    return (Wait(tok, resume), rx2);
}

function main(): i32 {
    var c1: i32 = tcp_connect(%d, %d);
    var c2: i32 = tcp_connect(%d, %d);
    if (c1 < 0) { return 81; }
    if (c2 < 0) { return 82; }
    if (tcp_send(c1, "GET /1 HTTP/1.1\r\nHost: x\r\n\r\n") < 0) { return 83; }
    if (tcp_send(c2, "GET /2 HTTP/1.1\r\nHost: x\r\n\r\n") < 0) { return 84; }
    var rx0: task.Reactor = task.reactor_new();
    var (s1, rx1) = start_fetch(rx0, c1);
    var (s2, rx2) = start_fetch(rx1, c2);
    var states: task.Step[] = [s1, s2];
    var (winner, result) = task.select(states, rx2);
    if (winner >= 0 && result == 1) { return 42; }
    return 86;
}`, host, port, host, port)

	dir := t.TempDir()
	progs := []struct {
		name string
		src  string
	}{
		{"run_await_all", runSrc},
		{"select_race", selectSrc},
	}
	backends := []struct {
		target string
		qemu   func(*testing.T) string
		run    func(qemu, bin string, args ...string) *exec.Cmd
	}{
		{"x86-64", x86QemuOrEmpty, runX86Bin},
		{"arm64", arm64QemuOrEmpty, runArm64Bin},
	}
	for _, p := range progs {
		p := p
		srcPath := filepath.Join(dir, p.name+".fern")
		if err := os.WriteFile(srcPath, []byte(p.src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		for _, be := range backends {
			be := be
			t.Run(p.name+"/"+be.target, func(t *testing.T) {
				qemu := be.qemu(t) // skips if no runner
				out := filepath.Join(dir, p.name+"_"+be.target+".bin")
				if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
					t.Fatalf("build failed: %v\n%s", err, o)
				}
				cmd := be.run(qemu, out)
				_ = cmd.Run()
				if code := cmd.ProcessState.ExitCode(); code != 42 {
					t.Errorf("%s/%s: std/task real-fd fan-out exit = %d, want 42", p.name, be.target, code)
				}
			})
		}
	}
}
