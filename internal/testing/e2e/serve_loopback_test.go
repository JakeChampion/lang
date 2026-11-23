package e2e

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const serveLoopbackSource = `import "std/http";
import "std/i32";
import "std/platform";
import "std/serve";

function main(): i32 {
  let port = i32.parse_i32(args()[1]).unwrap_or(0);
  let handler: (HttpRequest, platform.Host) => HttpResponse = (req: HttpRequest, host: platform.Host): HttpResponse => http.ok("here");
  return serve.exit_code(serve.run(port, serve.config().with_loopback(), handler));
}
`

// A server with with_loopback answers on 127.0.0.1, and takes no connection
// on another address of the machine.
func TestServeLoopbackOnly(t *testing.T) {
	host := e2eharness.HostSelfHostTarget()
	if host == "" {
		t.Skip("no self-host target runs natively on this machine")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "loopback.fern")
	if err := os.WriteFile(src, []byte(serveLoopbackSource), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "loopback")
	build := exec.Command(e2eharness.SelfHostCLI(t), "-target", host, "-o", bin, src, e2eharness.SelfHostStdlibRoot(t))
	build.Env = e2eharness.ChildEnv()
	if o, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, o)
	}
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := free.Addr().(*net.TCPAddr).Port
	free.Close()
	server := exec.Command(bin, strconv.Itoa(port))
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		server.Process.Kill()
		server.Wait()
	})
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	var answered bool
	for i := 0; i < 50 && !answered; i++ {
		if resp, err := http.Get(url); err == nil {
			resp.Body.Close()
			answered = resp.StatusCode == 200
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !answered {
		t.Fatal("the server did not answer on 127.0.0.1")
	}
	other := otherIPv4(t)
	if other == "" {
		t.Log("the machine has no address that is not loopback, so only 127.0.0.1 is checked")
		return
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(other, strconv.Itoa(port)), 2*time.Second)
	if err == nil {
		conn.Close()
		t.Fatalf("the server took a connection on %s", other)
	}
}

// otherIPv4 is an IPv4 address of the machine that is not loopback, or "".
func otherIPv4(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			return n.IP.String()
		}
	}
	return ""
}
