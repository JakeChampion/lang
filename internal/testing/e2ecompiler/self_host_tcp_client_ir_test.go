package e2ecompiler

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
		t.Fatalf("no free TCP port: %v", err)
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
	return fmt.Sprintf(`function main(): i32 {
    let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], %d, false);
    if (c < 0) { return 100; }
    if (tcp_pollable(c) != c) { tcp_close(c); return 102; }
    if (tcp_send_bytes(c, [112u8, 105u8, 110u8, 103u8]) < 0) { tcp_close(c); return 101; }
    let resp: u8[] = __alloc_u8(64);
    let n: i32 = tcp_recv_into(c, resp);
    tcp_close(c);
    return n;
}`, port)
}

// The TCP operations use the self-host IR path and its Fern runtime helpers.
// Native pollable conversion preserves the descriptor directly. A loopback
// round-trip checks connect, identity, send and recv, returning the six-byte
// reply length.
func TestSelfHostTcpClientIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	port := startTcpPongServer(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "probe")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	src := []byte(tcpClientIRProgram(port) + "\n")
	path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
	if path != "ir" {
		t.Fatalf("tcp client routed through %q path, want \"ir\"", path)
	}
	asm := runCapture(t, gcc, runner, driverBin, src)
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	if strings.Contains(string(asm), "__fern_tcp_pollable") {
		t.Fatal("native pollable identity still calls a runtime helper")
	}
	for _, sym := range []string{"__fn___fern_tcp_connect_with_u8", "__fn___fern_tcp_send_bytes_u8", "__fn___fern_tcp_recv_into_u8", "__fn___fern_tcp_close"} {
		if !strings.Contains(string(asm), "call "+sym+"\n") {
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
