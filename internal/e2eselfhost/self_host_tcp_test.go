package e2eselfhost

import (
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// tcpServerProgram is a one-shot HTTP server built from the self-hosted
// ARM64 emitter's TCP primitives: tcp_listen / tcp_accept / tcp_recv /
// tcp_send / tcp_close. It listens on PORT, accepts one connection, reads
// the request, sends a fixed HTTP/1.1 response, and exits 42 on the full
// success path (distinct small codes localise a failed step).
const tcpServerProgram = `function main(): i32 {
    var fd: i32 = tcp_listen(%d);
    if (fd < 0) { return 91; }
    var c: i32 = tcp_accept(fd);
    if (c < 0) { return 92; }
    var req: string = tcp_recv(c, 4096);
    if (req.len() == 0) { return 93; }
    var n: i32 = tcp_send(c, "HTTP/1.1 200 OK\r\nContent-Length: 11\r\n\r\nhello-world");
    tcp_close(c);
    tcp_close(fd);
    if (n < 0) { return 94; }
    return 42;
}`

// freeTCPPort asks the OS for an unused TCP port, then releases it. The
// compiled server re-binds it moments later — a benign TOCTOU window that
// is fine for a single-machine test.
func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestSelfHostTcpServerArm64 exercises the self-hosted ARM64 emitter's TCP
// socket primitives end-to-end: it compiles a one-shot HTTP server with
// the self-host arm64 emitter, runs it under qemu-aarch64 (which passes
// the socket syscalls through to the host), connects a Go client, and
// asserts the request/response round-trips and the server exits 42.
// CI-gated; skips cleanly without the aarch64 cross toolchain + qemu.
func TestSelfHostTcpServerArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	// Build the self-host arm64 emitter driver as an x86-64 host binary.
	prog, _, err := modload.Load(filepath.Join(dir, "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverBin := buildBin(t, x86gcc, dir, "driver", asm)

	port := freeTCPPort(t)
	serverSrc := fmt.Sprintf(tcpServerProgram, port)
	serverAsm := runCapture(t, x86gcc, x86runner, driverBin, []byte(serverSrc), "-target", "arm64-linux")
	if len(serverAsm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the tcp server")
	}
	serverBin := buildBin(t, arm64gcc, dir, "tcpserver", string(serverAsm))

	cmd := runArm64Bin(qemu, serverBin)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	// Hard watchdog: force-kill the (qemu) server after 30s no matter
	// what, so cmd.Wait() can never block the test indefinitely. Every
	// exit path below ends at cmd.Wait(), which the kill guarantees
	// returns. forceKilled distinguishes "served and exited" from "hung".
	var forceKilled bool
	wd := time.AfterFunc(30*time.Second, func() {
		forceKilled = true
		_ = cmd.Process.Kill()
	})

	// Dial with retry — the server (under qemu) needs a moment to bind.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	var conn net.Conn
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		c, derr := net.DialTimeout("tcp", addr, time.Second)
		if derr == nil {
			conn = c
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if conn != nil {
		if _, werr := conn.Write([]byte("GET /hello HTTP/1.1\r\nHost: x\r\n\r\n")); werr != nil {
			t.Errorf("client write: %v", werr)
		}
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		resp, rerr := io.ReadAll(conn)
		conn.Close()
		if rerr != nil {
			t.Errorf("client read: %v", rerr)
		}
		got := string(resp)
		if want := "hello-world"; !strings.Contains(got, want) {
			t.Errorf("response missing %q; got:\n%s", want, got)
		}
		if want := "HTTP/1.1 200 OK"; !strings.Contains(got, want) {
			t.Errorf("response missing status line %q; got:\n%s", want, got)
		}
	} else {
		t.Errorf("could not connect to self-host tcp server on %s", addr)
	}

	// The server returns after one connection; the watchdog bounds this.
	waitErr := cmd.Wait()
	wd.Stop()
	if forceKilled {
		t.Fatalf("self-host tcp server had to be force-killed (hung): %v", waitErr)
	}
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("self-host tcp server exited %d, want 42 (91=listen,92=accept,93=recv-empty,94=send)", code)
	}
}
