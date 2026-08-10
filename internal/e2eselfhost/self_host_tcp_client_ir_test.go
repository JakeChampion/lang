package e2eselfhost

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// startTcpPongServer opens a loopback TCP listener that, for each connection,
// reads the request and writes a fixed 6-byte reply ("pong!!"), then closes.
// Returns the port and registers cleanup. Used by the self-host tcp-client IR
// tests (the self-host-compiled client connects + sends + recvs against it).
func startTcpPongServer(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
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
				_, _ = c.Write([]byte("pong!!"))
			}(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// tcpClientIRProgram builds a self-host program that connects to 127.0.0.1:port,
// checks tcp_pollable identity, sends "ping", recvs the reply, and returns its
// length (the server replies with 6 bytes, so a healthy round-trip exits 6).
func tcpClientIRProgram(port int) string {
	// 127.0.0.1 in network byte order = 127 | (1 << 24) = 16777343.
	const host = 127 | (1 << 24)
	return fmt.Sprintf(`function main(): i32 {
    var host: i32 = %d;
    var c: i32 = tcp_connect(host, %d);
    if (c < 0) { return 100; }
    if (tcp_pollable(c) != c) { tcp_close(c); return 102; }
    var req: string = "ping";
    if (tcp_send(c, req) < 0) { tcp_close(c); return 101; }
    var resp: string = tcp_recv(c, 64);
    tcp_close(c);
    return resp.len();
}`, host, port)
}

// TestSelfHostTcpClientIRX86_64 is slice 4 of putting async on the self-hosted
// compiler's IR path (docs/ASYNC-SELFHOST-IR.md): the tcp_* CLIENT family
// (tcp_connect / tcp_send / tcp_recv / tcp_close / tcp_pollable) now lowers to
// dedicated IR ops on the self-host x86-64 IR backend, emitting calls into the
// __fern_tcp_* runtime helpers (x86 had NO tcp at all before this). recv/send
// use the SELF-HOST string ABI (a 16-byte box {data@0, len@8}), mirrored from
// the proven self-host arm64 bodies; connect/pollable mirror the native socket
// helpers. A tcp module is now IR-ELIGIBLE rather than failing to link.
//
// A real loopback round-trip against a Go server proves the whole path:
// connect succeeds, pollable is the fd (identity), send writes the request,
// recv reads the 6-byte reply, so the program exits 6.
func TestSelfHostTcpClientIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	port := startTcpPongServer(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "probe")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	src := []byte(tcpClientIRProgram(port) + "\n")
	path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
	if path != "ir" {
		t.Fatalf("tcp client routed through %q path, want \"ir\"", path)
	}
	asm := runCapture(t, gcc, runner, driverBin, src)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	for _, sym := range []string{"call __fn___fern_tcp_connect", "call __fn___fern_tcp_send", "call __fn___fern_tcp_recv", "call __fn___fern_tcp_close", "call __fern_tcp_pollable"} {
		if !strings.Contains(string(asm), sym) {
			t.Errorf("emitted asm missing %q (tcp op did not lower through the IR path)", sym)
		}
	}
	progBin := buildBin(t, gcc, dir, "tcp_client", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 6 {
		t.Errorf("tcp round-trip exited %d, want 6 (len of \"pong!!\")", code)
	}
}
