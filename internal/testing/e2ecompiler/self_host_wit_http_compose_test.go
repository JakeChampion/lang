package e2ecompiler

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSelfHostComposeHttpFromProxyWorld gates the self-host composer's
// incoming-handler shape (wit_compose.compose_http): a hand-written reactor
// core that exports `wasi:http/incoming-handler@0.2.0#handle` and imports the
// wasi:http/types constructors, the outparam setter (a memory lowering) and a
// `[resource-drop]` is composed against the embedded proxy world by the
// self-host, validates under wasm-tools, and answers a request under
// `wasmtime serve` with the 200 it sets. The drop is what the cli/run shape
// never needed: the resource has to be surfaced as a component type for the
// canon resource.drop, and this is the first composition to do so. The
// component imports wasi:http/types alone: the core touches no stream, so
// wasi:io/streams, which only the body methods it never calls read, is not
// declared either.
func TestSelfHostComposeHttpFromProxyWorld(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	coreWat := filepath.Join(dir, "core.wat")
	if err := os.WriteFile(coreWat, []byte(minimalHttpHandlerCore), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(wasmtools, "parse", coreWat, "-o", filepath.Join(dir, "core.bin")).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse: %v\n%s", err, out)
	}

	driverWat := witCompileToWat(t, dir, "driver", withPrintInt(selfHostComposeHttpDriver))
	out, err := exec.Command(wasmtime, "run", "--dir", dir, driverWat).CombinedOutput()
	if err != nil {
		t.Fatalf("run driver: %v\n%s", err, out)
	}
	var comp []byte
	for _, tok := range strings.Fields(string(out)) {
		n, err := strconv.Atoi(tok)
		if err != nil {
			t.Fatalf("bad byte %q in driver output:\n%s", tok, out)
		}
		comp = append(comp, byte(n))
	}
	component := filepath.Join(dir, "handler.wasm")
	if err := os.WriteFile(component, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	if vout, err := exec.Command(wasmtools, "validate", component).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, vout)
	}
	wit, err := exec.Command(wasmtools, "component", "wit", component).CombinedOutput()
	if err != nil {
		t.Fatalf("component wit: %v\n%s", err, wit)
	}
	for _, want := range []string{"export wasi:http/incoming-handler@0.2.0", "import wasi:http/types@0.2.0"} {
		if !strings.Contains(string(wit), want) {
			t.Errorf("component WIT lacks %q:\n%s", want, wit)
		}
	}
	// The world's imports bound the component's: an interface the core
	// never reaches is not declared, so `wasmtime serve` without `-S
	// config` takes the component.
	for _, unused := range []string{"wasi:config/store", "wasi:cli/stdout", "wasi:random/random", "wasi:io/streams"} {
		if strings.Contains(string(wit), unused) {
			t.Errorf("component imports %s, which the core never reaches:\n%s", unused, wit)
		}
	}
	status, _, _ := serveComponent(t, wasmtime, component, "GET", "/", "")
	if status != http.StatusOK {
		t.Fatalf("GET / = %d; want 200", status)
	}
}

// minimalHttpHandlerCore answers every request with an empty 200: drop the
// request, build a response over fresh fields, and hand it to the outparam
// as `ok`. response-outparam.set's `result<own<outgoing-response>,
// error-code>` flattens to the nine core params (the i64 from error-code's
// option<u64> arm).
const minimalHttpHandlerCore = `(module
  (import "wasi:http/types@0.2.0" "[resource-drop]incoming-request" (func $req_drop (param i32)))
  (import "wasi:http/types@0.2.0" "[constructor]fields" (func $fields_new (result i32)))
  (import "wasi:http/types@0.2.0" "[constructor]outgoing-response" (func $resp_new (param i32) (result i32)))
  (import "wasi:http/types@0.2.0" "[static]response-outparam.set" (func $outparam_set (param i32 i32 i32 i32 i64 i32 i32 i32 i32)))
  (memory (export "memory") 1)
  (func (export "wasi:http/incoming-handler@0.2.0#handle") (param $req i32) (param $out i32)
    (call $req_drop (local.get $req))
    (call $outparam_set (local.get $out) (i32.const 0) (call $resp_new (call $fields_new))
      (i32.const 0) (i64.const 0) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0))))
`

// selfHostComposeHttpDriver composes core.bin against the embedded proxy
// world and prints the component as decimal bytes; a refusal is exit 3 with
// the reason on stderr.
const selfHostComposeHttpDriver = `
function main(): i32 {
    match (read_file_bytes("core.bin")) {
        Ok(s) => {
            let core: i32[] = [];
            let i: i32 = 0;
            while (i < s.len()) { core = core.append((s[i] as i32)); i = i + 1; }
            let tbody: i32[] = wit_section_body(blob_to_bytes(proxy_world_payload()), 7);
            let c: Composed = compose_http(tbody, core);
            if (c.refused.len() > 0) { eprint(c.refused); return 3; }
            let j: i32 = 0;
            while (j < c.bytes.len()) { print_int(c.bytes[j]); write("\n"); j = j + 1; }
            return 0;
        },
        Err(e) => { return 1; }
    }
    return 2;
}
`

// serveComponent serves a wasi:http component under `wasmtime serve` on a
// free port, sends one request, and returns its status, headers and body.
func serveComponent(t *testing.T, wasmtime, component, method, path, body string) (int, http.Header, string) {
	t.Helper()
	return serveComponentHeaders(t, wasmtime, component, method, path, body, nil)
}

// serveComponentHeaders is serveComponent with request headers.
func serveComponentHeaders(t *testing.T, wasmtime, component, method, path, body string, headers map[string]string) (int, http.Header, string) {
	t.Helper()
	status, hdr, got, _ := serveComponentLogged(t, wasmtime, component, method, path, body, headers, "")
	return status, hdr, got
}

// serveComponentLogged is serveComponentHeaders that also returns what
// `wasmtime serve` wrote, once that holds `logged`: the guest's stderr
// arrives on its own stream, after the response it was written before.
func serveComponentLogged(t *testing.T, wasmtime, component, method, path, body string, headers map[string]string, logged string) (int, http.Header, string, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	srv := exec.Command(wasmtime, "serve", "--addr", addr, component)
	var slog lockedBuffer
	srv.Stdout = &slog
	srv.Stderr = &slog
	if err := srv.Start(); err != nil {
		t.Fatalf("start wasmtime serve: %v", err)
	}
	defer func() {
		_ = srv.Process.Kill()
		_, _ = srv.Process.Wait()
	}()

	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	var resp *http.Response
	for {
		req, err := http.NewRequest(method, fmt.Sprintf("http://%s%s", addr, path), strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err = client.Do(req)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s %s: %v\nwasmtime serve:\n%s", method, path, err, slog.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 500 {
		t.Logf("wasmtime serve:\n%s", slog.String())
	}
	for wait := time.Now().Add(2 * time.Second); logged != "" && !strings.Contains(slog.String(), logged) && time.Now().Before(wait); {
		time.Sleep(50 * time.Millisecond)
	}
	return resp.StatusCode, resp.Header, string(got), slog.String()
}

// lockedBuffer is a buffer `wasmtime serve` writes while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
