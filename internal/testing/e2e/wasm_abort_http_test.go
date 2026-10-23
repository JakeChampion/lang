package e2e

import (
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWasiHTTPAbortMessage(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "handler.fern")
	program := `import "std/http";
import "std/serve";
import "std/platform";
@noinline function fail(i: i32): string { let a: string[] = ["only"]; return a[i]; }
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
  return http.ok(fail(req.path.len()));
}
`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	component := filepath.Join(dir, "handler.wasm")
	if out, err := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi-http", "-o", component, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	logPath := filepath.Join(dir, "stderr")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(freeLoopbackPort(t)))
	srv := exec.Command("wasmtime", "serve", "--addr", addr, component)
	srv.Stderr = log
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = srv.Process.Kill()
		_ = srv.Wait()
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			out, _ := os.ReadFile(logPath)
			t.Fatalf("server did not start: %v\n%s", err, out)
		}
		time.Sleep(20 * time.Millisecond)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	if response, err := client.Get("http://" + addr + "/"); err == nil {
		response.Body.Close()
		if response.StatusCode < 500 {
			t.Fatalf("request must fail after the trap, got status %d", response.StatusCode)
		}
	}
	out, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "fern: array index out of range\n") {
		t.Fatalf("proxy abort did not report its cause:\n%s", out)
	}
}
