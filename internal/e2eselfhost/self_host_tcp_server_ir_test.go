package e2eselfhost

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

	// No port is named anywhere: the server binds 0, asks the socket which
	// port it got, and prints it. That replaces probing the host for a free
	// port and re-binding it in the guest, which raced anything else on the
	// machine between the probe closing and the server binding.
	//
	// `port_str` is spelled out rather than imported from core/int because
	// this driver parses ONE module off stdin and resolves no imports.
	prog := `function port_str(n: i32): string {
    var s: string = "";
    var v: i32 = n;
    while (v > 0) {
        s = chr(48 + (v % 10)) + s;
        v = v / 10;
    }
    return s;
}

function main(): i32 {
    var fd: i32 = tcp_listen(0);
    if (fd < 0) { return 91; }
    var port: i32 = tcp_local_port(fd);
    if (port <= 0) { return 97; }
    print(port_str(port));
    var lfds: i32[] = [fd];
    if (poll(lfds, 10000) < 0) { return 95; }
    var c: i32 = tcp_accept(fd);
    if (c < 0) { return 92; }
    var cfds: i32[] = [c];
    if (poll(cfds, 10000) < 0) { return 96; }
    var req: u8[] = tcp_recv(c, 4096);
    if (req.len() == 0) { return 93; }
    var n: i32 = tcp_send(c, "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello");
    tcp_close(c);
    tcp_close(fd);
    if (n < 0) { return 94; }
    return 42;
}`

	asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the TCP server program")
	}
	progBin := buildBin(t, gcc, dir, "tcp_server", string(asm))

	cmd := exec.Command(progBin)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	// The announced port is also the readiness signal — it is printed after
	// the listen, so there is nothing to poll for and no dial to retry. The
	// one connection the server accepts IS the request.
	port := readAnnouncedPort(t, stdout)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		t.Fatalf("dial the announced port %d: %v", port, err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	buf := make([]byte, 256)
	n, _ := bufio.NewReader(conn).Read(buf)
	resp := string(buf[:n])
	conn.Close()

	if !strings.Contains(resp, "hello") {
		t.Errorf("response = %q, want it to contain %q (self-host IR tcp_listen/accept serve path, #4371)", resp, "hello")
	}
	_ = cmd.Wait()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("self-host TCP server exit = %d, want 42 (listen/local_port/accept/recv/send on the announced port %d; #4371)", code, port)
	}
}

// readAnnouncedPort reads the port a self-announcing server prints on its
// first stdout line. A server that dies before printing gives an EOF here
// rather than a dial that times out against a port nobody bound, so the
// failure names the real cause.
func readAnnouncedPort(t *testing.T, stdout io.Reader) int {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		ch <- result{line, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("reading the server's port line: %v", r.err)
		}
		port, err := strconv.Atoi(strings.TrimSpace(r.line))
		if err != nil || port <= 0 || port > 65535 {
			t.Fatalf("server announced %q, want a port", r.line)
		}
		return port
	case <-time.After(20 * time.Second):
		t.Fatal("server never announced a port")
		return 0
	}
}
