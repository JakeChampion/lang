package e2e

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestWasiHTTPRequestStringsAreOwned(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "handler.fern")
	if err := os.WriteFile(src, []byte(`
import "std/http";
import "std/serve";
import "std/platform";
function touch(p: string): i32 {
    let held: string[] = [p];
    return held.len();
}
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let n: i32 = touch(req.path) + touch(req.method);
    let filler: string = "filler-" + n.to_string();
    match (req.headers.get("x-echo-header")) {
        Some(v) => {
            n = n + touch(v);
            let more: string = "more-filler-" + n.to_string();
            return http.ok(req.method + "|" + req.path + "|" + v + "|" + filler + "|" + more);
        },
        None => { return http.text(400, "no x-echo-header"); },
    }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	component := filepath.Join(dir, "handler.wasm")
	if out, err := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi-http", "-o", component, src).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	srv, err := e2eharness.StartWasmtimeServe(wasmtime, component)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Stop() })

	client := &http.Client{Timeout: 5 * time.Second}
	for i, value := range []string{"first-value-long", "second"} {
		req, _ := http.NewRequest("PROPFINDX", "http://"+srv.Addr+"/abcdefg", nil)
		req.Header.Set("x-echo-header", value)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request %d: %v\nstderr:\n%s", i, err, srv.Log())
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		want := "PROPFINDX|/abcdefg|" + value + "|filler-2|more-filler-3"
		if resp.StatusCode != 200 || string(body) != want {
			t.Errorf("request %d: status %d body %q, want 200 %q\nstderr:\n%s", i, resp.StatusCode, body, want, srv.Log())
		}
	}
}
