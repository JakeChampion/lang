package e2e

import (
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The wasi:http wrapper hands the handler owned strings, so a retain and its
// release change nothing but the string's own count (#11479).
//
// The method, the path and every header name and value used to reach Fern
// code as (data, len) pairs over buffers with no rc header: the count a retain
// and a release read was whatever word sat 8 bytes before the data. For a path
// of eight bytes or fewer that word is the zeroed tail of the host's buffer, so
// `touch(req.path)` took it from 0 to 1 and back, freeing the path while the
// request still held it, and the next allocation (`filler`) read back in its
// place. Header names showed the same fault as a value shifted by one byte
// (`"irst\x00"` for `"first"`) once HeaderMap.append kept a name it was handed
// instead of a lowercased copy.
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

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(freeLoopbackPort(t)))
	srv := exec.Command(wasmtime, "serve", "--addr", addr, component)
	var serr strings.Builder
	srv.Stderr = &serr
	if err := srv.Start(); err != nil {
		t.Fatalf("wasmtime serve: %v", err)
	}
	t.Cleanup(func() {
		srv.Process.Kill()
		srv.Wait()
	})

	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for i, value := range []string{"first-value-long", "second"} {
		var resp *http.Response
		for {
			req, _ := http.NewRequest("PROPFINDX", "http://"+addr+"/abcdefg", nil)
			req.Header.Set("x-echo-header", value)
			var err error
			if resp, err = client.Do(req); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("request %d: %v\nstderr:\n%s", i, err, serr.String())
			}
			time.Sleep(50 * time.Millisecond)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		want := "PROPFINDX|/abcdefg|" + value + "|filler-2|more-filler-3"
		if resp.StatusCode != 200 || string(body) != want {
			t.Errorf("request %d: status %d body %q, want 200 %q\nstderr:\n%s", i, resp.StatusCode, body, want, serr.String())
		}
	}
}

// freeLoopbackPort asks the kernel for a free TCP port and releases it, for
// a server such as `wasmtime serve` that binds its own address and cannot be
// handed a listener.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no free TCP port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	return port
}
