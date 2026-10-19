package e2ecompiler

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// tcp_recv_into reads into the caller's u8[] (D9, #5714): every byte value
// passes through unmodified, including 0x00; an empty buffer reads nothing;
// and 0 is the end of the stream, which no error answers. The 5-byte
// payload into a 4096-byte buffer also pins the short-read contract: the
// count is the bytes read, not the capacity. The arm64 and wasm helpers are
// covered by their own tcp e2e suites.
func TestSelfHostTcpRecvIntoBytesX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no free TCP port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	// Distinct exit codes localise a failed step: 9x = setup,
	// 97 = empty buffer, 93 = short-read count, 8x = byte value,
	// 86 = end of stream, 42 = all paths held.
	src := fmt.Sprintf(`function main(): i32 {
    let fd: i32 = tcp_listen(%d);
    if (fd < 0) { return 91; }
    let c: i32 = tcp_accept(fd);
    if (c < 0) { return 92; }
    let none: u8[] = [];
    if (tcp_recv_into(c, none) != 0) { return 97; }
    let req: u8[] = __alloc_u8(4096);
    if (tcp_recv_into(c, req) != 5) { return 93; }
    if (req[0] != 0) { return 81; }
    if (req[1] != 255) { return 82; }
    if (req[2] != 128) { return 83; }
    if (req[3] != 10) { return 84; }
    if (req[4] != 65) { return 85; }
    if (tcp_recv_into(c, req) != 0) { return 86; }
    tcp_close(c);
    tcp_close(fd);
    return 42;
}`, port)

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "server.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := runX86_64Bin(cli.runner, cli.x86Binary(t, srcPath, "FERN_STRICT_IR=1"))
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	payload := []byte{0x00, 0xFF, 0x80, 0x0A, 0x41}
	deadline := time.Now().Add(10 * time.Second)
	sent := false
	for time.Now().Before(deadline) {
		conn, derr := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if derr != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		if _, werr := conn.Write(payload); werr != nil {
			conn.Close()
			t.Fatalf("write payload: %v", werr)
		}
		conn.Close() // close-after-write drives the EOF-sentinel read
		sent = true
		break
	}
	if !sent {
		t.Fatal("could not connect to server within deadline")
	}

	_ = cmd.Wait()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("server exit = %d, want 42 (byte fidelity + sentinel paths)", code)
	}
}
