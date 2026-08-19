package e2eselfhost

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

// #4371: the SERVER half of the edge-handler use case — tcp_listen / tcp_accept —
// must lower on the self-host x86-64 IR path. The client half (tcp_connect / send /
// recv / close / pollable) already lowered; tcp_listen / tcp_accept bailed the IR
// path (irlower had no op for them, asmcore didn't type them), so std/tcp's serve
// loop bailed — and the legacy AST backend it fell to had no x86
// __fern_tcp_listen body, so it wouldn't link. This drives a poll-driven one-shot
// server through the
// self-host x86-64 IR driver (asm_run) end-to-end: compile → link → serve one real
// TCP connection → respond → exit 42. A successful run PROVES the IR path was taken,
// since nothing else emits the listen/accept helpers.
//
// Poll-driven (like internal/e2e's native TestPollDrivenTcpServerX86_64) so accept
// and recv never block the test: poll the listener before accept, the connection
// before recv. Distinct small exit codes localise a failed step.
func TestSelfHostTcpServerIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host TCP server test runs host-native only (avoids qemu socket nuances)")
	}
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	// Pick a free port, then let the compiled server re-bind it.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	prog := fmt.Sprintf(`function main(): i32 {
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

	asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the TCP server program")
	}
	progBin := buildBin(t, gcc, dir, "tcp_server", string(asm))

	cmd := exec.Command(progBin)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	// Retry the dial to absorb the bind race; the first successful connection
	// IS the request (the server handles exactly one, then exits).
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
		t.Errorf("response = %q, want it to contain %q (self-host IR tcp_listen/accept serve path, #4371)", resp, "hello")
	}
	_ = cmd.Wait()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("self-host TCP server exit = %d, want 42 (tcp_listen=%d..listen/accept/recv/send steps; #4371)", code, port)
	}
}
