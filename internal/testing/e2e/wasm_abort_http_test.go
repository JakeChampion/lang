package e2e

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
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
	srv, err := e2eharness.StartWasmtimeServe("wasmtime", component)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Stop() })
	client := &http.Client{Timeout: 5 * time.Second}
	if response, err := client.Get("http://" + srv.Addr + "/"); err == nil {
		response.Body.Close()
		if response.StatusCode < 500 {
			t.Fatalf("request must fail after the trap, got status %d", response.StatusCode)
		}
	}
	out := srv.Stop()
	if !strings.Contains(out, "fern: array index out of range\n") {
		t.Fatalf("proxy abort did not report its cause:\n%s", out)
	}
}
