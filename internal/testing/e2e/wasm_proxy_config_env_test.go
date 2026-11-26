package e2e

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestWasmProxyConfigEnv reads the platform config in a wasm32-wasi-http handler,
// which is wasi:config. A set variable gives Some of its value, and an unset
// one gives None.
func TestWasmProxyConfigEnv(t *testing.T) {
	for _, tool := range []string{"wasm-tools", "wasmtime"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH; skipping preview-2 e2e")
		}
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "config.fern")
	src := `
import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let name = if (req.path == "/set") { "GREETING" } else { "UNSET_NAME" };
    match (plat.config(name)) {
        Some(v) => { return http.ok("some:" + v); },
        None => { return http.ok("none"); },
    }
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	component := filepath.Join(dir, "config.component.wasm")
	if out, err := exec.Command(buildFernCLI(t), "-target", "wasm32-wasi-http", "-o", component, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi-http: %v\n%s", err, out)
	}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()
	run := exec.Command("wasmtime", "serve", "--addr", addr, "-S", "config", "-S", "config-var=GREETING=hello", component)
	var serr bytes.Buffer
	run.Stderr = &serr
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		run.Process.Kill()
		run.Wait()
	})
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for _, c := range []struct{ path, want string }{{"/set", "some:hello"}, {"/unset", "none"}} {
		var resp *http.Response
		for {
			resp, err = client.Get("http://" + addr + c.path)
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %v\nstderr:\n%s", c.path, err, serr.String())
			}
			time.Sleep(50 * time.Millisecond)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != c.want {
			t.Errorf("%s: status %d body %q, want 200 %q\nstderr:\n%s", c.path, resp.StatusCode, body, c.want, serr.String())
		}
	}
}
