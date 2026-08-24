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

// tcp_recv returns u8[] (D9, #5714): every byte value passes through
// unmodified — including 0x00, which the old string shape could carry
// but consumers routinely truncated at — and the empty array is the
// sole sentinel for EOF, error, and max <= 0 alike. The 5-byte payload
// against max=4096 also pins the short-read contract: len is the
// actual count, not the capacity. x86-64 host-native; the arm64 and
// wasm helpers are covered by their own tcp e2e suites.
func TestTcpRecvBytesX86_64(t *testing.T) {
	if qemu := x86QemuOrEmpty(t); qemu != "" {
		t.Skip("tcp_recv byte test runs host-native only")
	}
	bin := buildFernCLI(t)

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free TCP port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	// Distinct exit codes localise a failed step: 9x = setup,
	// 97 = max<=0 sentinel, 93 = short-read len, 8x = byte value,
	// 86 = EOF sentinel, 42 = all paths held.
	src := fmt.Sprintf(`function main(): i32 {
    var fd: i32 = tcp_listen(%d);
    if (fd < 0) { return 91; }
    var c: i32 = tcp_accept(fd);
    if (c < 0) { return 92; }
    var zero: u8[] = tcp_recv(c, 0);
    if (zero.len() != 0) { return 97; }
    var req: u8[] = tcp_recv(c, 4096);
    if (req.len() != 5) { return 93; }
    if (req[0] != 0) { return 81; }
    if (req[1] != 255) { return 82; }
    if (req[2] != 128) { return 83; }
    if (req[3] != 10) { return 84; }
    if (req[4] != 65) { return 85; }
    var eofb: u8[] = tcp_recv(c, 100);
    if (eofb.len() != 0) { return 86; }
    tcp_close(c);
    tcp_close(fd);
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
