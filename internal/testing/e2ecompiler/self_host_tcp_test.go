package e2ecompiler

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

const tcpServerProgram = `function main(): i32 {
    let fd: i32 = tcp_listen(%d);
    if (fd < 0) { return 91; }
    let c: i32 = tcp_accept(fd);
    if (c < 0) { return 92; }
    let req: u8[] = __alloc_u8(4096);
    if (tcp_recv_into(c, req) <= 0) { return 93; }
    let n: i32 = tcp_send_bytes(c, [72u8, 84u8, 84u8, 80u8, 47u8, 49u8, 46u8, 49u8, 32u8, 50u8, 48u8, 48u8, 32u8, 79u8, 75u8, 13u8, 10u8, 67u8, 111u8, 110u8, 116u8, 101u8, 110u8, 116u8, 45u8, 76u8, 101u8, 110u8, 103u8, 116u8, 104u8, 58u8, 32u8, 49u8, 49u8, 13u8, 10u8, 13u8, 10u8, 104u8, 101u8, 108u8, 108u8, 111u8, 45u8, 119u8, 111u8, 114u8, 108u8, 100u8]);
    tcp_close(c);
    tcp_close(fd);
    if (n < 0) { return 94; }
    return 42;
}`

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestSelfHostTcpServerArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
	var forceKilled bool
	wd := time.AfterFunc(30*time.Second, func() {
		forceKilled = true
		_ = cmd.Process.Kill()
	})

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

	waitErr := cmd.Wait()
	wd.Stop()
	if forceKilled {
		t.Fatalf("self-host tcp server had to be force-killed (hung): %v", waitErr)
	}
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("self-host tcp server exited %d, want 42 (91=listen,92=accept,93=recv-empty,94=send)", code)
	}
}
