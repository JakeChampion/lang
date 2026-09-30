package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// tcp_serve_opts (#9853): the accept loop's listener takes the backlog and
// SO_REUSEPORT the caller asks for instead of tcp_listen's fixed 128 and
// one listener per port. The proof that the option reached the kernel is
// a second SO_REUSEPORT socket binding the served port while the loop
// holds it, which a plain listener refuses with EADDRINUSE; the loop
// still answers 200 through the first.
const serveOptsSrc = `
import "std/http";
import "std/time";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.http_response_ok("ok");
}
function main(): i32 {
    var opts: tcp.ServeOptions = tcp.ServeOptions { ...tcp.serve_options(), backlog: 4, reuse_port: true };
    return tcp.tcp_serve_opts(%d, opts, handle);
}`

func TestServeOptionsX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, fmt.Sprintf(serveOptsSrc, port))
	_, _ = startSupervisedServer(t, bin, runner)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	e2eharness.WaitServerReady(t, addr, 10*time.Second)

	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, soReusePort, 1); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Port: port, Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatalf("a second SO_REUSEPORT socket could not bind the served port, so reuse_port did not reach the listener: %v", err)
	}

	if resp := e2eharness.HTTPRoundTrip(t, addr, "/ok", 3*time.Second); !e2eharness.ContainsStatus200(resp) {
		t.Fatalf("the loop did not answer 200:\n%s", resp)
	}
}

// A client past `max_connections_per_ip` (#9854) has its next connection
// closed as it is accepted, on the native backend and the interpreter;
// the scenario is e2eharness's, shared with the self-host twin.
func TestServePerIPCapX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.PerIPCapServerSource(port))
	startSupervisedServer(t, bin, runner)
	e2eharness.CheckPerIPCap(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestServePerIPCapInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	port := freeLoopbackPort(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "srv.fern")
	if err := os.WriteFile(srcPath, []byte(e2eharness.PerIPCapServerSource(port)), 0o644); err != nil {
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
	e2eharness.CheckPerIPCap(t, fmt.Sprintf("127.0.0.1:%d", port))
}

// soReusePort is SO_REUSEPORT, which Go's syscall package spells only on
// the BSDs: 15 on Linux.
const soReusePort = 15
