package e2e

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// A response larger than a socket's send buffer on a non-blocking
// connection: the serve loop keeps what the kernel did not take, watches
// the connection for writability and finishes the write on later waits,
// so the client reads the whole body (#9853, the write side). The client
// asks for the connection to close and reads to end of stream, so the
// loop must also hold the close until the deferred write has drained.
const serveLargeResponseSrc = `
import "std/http";
import "std/string";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.http_response_ok("x".repeat(1500000));
}
function main(): i32 {
    return tcp.tcp_serve(%d, handle);
}`

func readWholeResponse(t *testing.T, addr string) (string, string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /big HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v (got %d bytes)", err, len(got))
	}
	head, body, ok := strings.Cut(string(got), "\r\n\r\n")
	if !ok {
		t.Fatalf("no header terminator in %d bytes", len(got))
	}
	return head, body
}

func checkLargeResponse(t *testing.T, addr string) {
	t.Helper()
	for i := 0; i < 2; i++ {
		head, body := readWholeResponse(t, addr)
		if !containsStatus200(head) {
			t.Fatalf("round %d: not a 200:\n%s", i, head)
		}
		if len(body) != 1500000 || strings.Trim(body, "x") != "" {
			t.Fatalf("round %d: body is %d bytes, want 1500000 of x", i, len(body))
		}
	}
}

func TestServeLargeResponseX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, fmt.Sprintf(serveLargeResponseSrc, port))
	_, _ = startSupervisedServer(t, bin, runner)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	e2eharness.WaitServerReady(t, addr, 10*time.Second)
	checkLargeResponse(t, addr)
}

func TestServeLargeResponseInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	port := freeLoopbackPort(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "srv.fern")
	if err := os.WriteFile(srcPath, []byte(fmt.Sprintf(serveLargeResponseSrc, port)), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", srcPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start interp server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	e2eharness.WaitServerReady(t, addr, 30*time.Second)
	checkLargeResponse(t, addr)
}
