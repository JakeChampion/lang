package e2e

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A poll-driven one-shot TCP server proves the `poll` builtin
// multiplexes REAL network sockets end-to-end (so far poll was only
// tested on files + timerfds): the server waits on the listener fd
// with `poll` before accept, then on the connection fd with `poll`
// before recv — no new non-blocking builtins, since a blocking
// accept/recv won't block once poll reports the fd ready. This is the
// shape a reactor-driven edge handler uses (docs/ASYNC-IMPLEMENTATION-PLAN.md
// Phase 1c). x86-64 host-native (no qemu); poll on arm64 is the same
// syscall path, validated deterministically on files + timers.
func TestPollDrivenTcpServerX86_64(t *testing.T) {
	if qemu := x86QemuOrEmpty(t); qemu != "" {
		t.Skip("poll TCP server test runs host-native only (avoids qemu socket nuances)")
	}
	bin := buildFernCLI(t)

	// Pick a free port, then let the compiled server re-bind it.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	// One-shot server: poll the listener for a pending connection,
	// accept, poll the connection for the request, recv, respond, exit
	// 42. Distinct small codes localise a failed step.
	src := fmt.Sprintf(`function main(): i32 {
    var fd: i32 = tcp_listen(%d);
    if (fd < 0) { return 91; }
    var lfds: i32[] = [fd];
    if (poll(lfds, 10000) < 0) { return 95; }
    var c: i32 = tcp_accept(fd);
    if (c < 0) { return 92; }
    var cfds: i32[] = [c];
    if (poll(cfds, 10000) < 0) { return 96; }
    var req: string = tcp_recv(c, 4096);
    if (req.len() == 0) { return 93; }
    var n: i32 = tcp_send(c, "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello");
    tcp_close(c);
    tcp_close(fd);
    if (n < 0) { return 94; }
    return 42;
}`, port)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "server.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "server.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, o)
	}

	cmd := exec.Command(out)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	// Retry the dial to absorb the bind race; the first successful
	// connection IS the request (the server handles exactly one).
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	var resp string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, derr := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if derr != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
		buf := make([]byte, 256)
		n, _ := bufio.NewReader(conn).Read(buf)
		resp = string(buf[:n])
		conn.Close()
		break
	}

	if !strings.Contains(resp, "hello") {
		t.Errorf("response = %q, want it to contain %q", resp, "hello")
	}
	_ = cmd.Wait()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("server exit = %d, want 42 (poll-driven accept+recv path)", code)
	}
}
