package e2e

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// httpHandlerSetResponseProg is a bring-your-own wasi:http handler that builds
// a 200 response and hands it to response-outparam.set. The set extern is
// declared with its canonical flattened params — result<own<outgoing-response>,
// error-code> + the outparam handle flatten to 9 core values
// [i32 i32 i32 i32 i64 i32 i32 i32 i32] (slot 4 is i64, widened by error-code's
// option<u64> arm) — and the call passes Ok (disc 0, the response handle, the
// rest zero). The composer lowers set as a gMem trampoline (error-code carries
// heap → KindMem) using the core import's own 9-param signature.
const httpHandlerSetResponseProg = `@import("wasi:http/types@0.2.0", "incoming-request")
resource IncomingRequest;
@import("wasi:http/types@0.2.0", "response-outparam")
resource ResponseOutparam;
@import("wasi:http/types@0.2.0", "fields")
resource Fields;
@import("wasi:http/types@0.2.0", "outgoing-response")
resource OutgoingResponse;

@import("wasi:http/types@0.2.0", "[constructor]fields")
function fields_new(): own Fields;
@import("wasi:http/types@0.2.0", "[constructor]outgoing-response")
function response_new(headers: own Fields): own OutgoingResponse;
@import("wasi:http/types@0.2.0", "[static]response-outparam.set")
function set_response(out: own ResponseOutparam, disc: i32, resp: own OutgoingResponse, a: i32, b: i64, c: i32, d: i32, e: i32, f: i32): void;

@export("wasi:http/incoming-handler@0.2.0", "handle")
function on_request(request: own IncomingRequest, response_out: own ResponseOutparam): void {
	var headers: own Fields = fields_new();
	var resp: own OutgoingResponse = response_new(headers);
	set_response(response_out, 0, resp, 0, 0 as i64, 0, 0, 0, 0);
	return;
}`

// minimalHttpProxyWorld writes a WIT dir (the real wasi deps + a world that
// imports only wasi:http/types and exports wasi:http/incoming-handler) and
// returns the decoded world. Importing only http/types pulls in exactly the
// transitive proxy imports (io / clocks) — NOT filesystem/sockets — so the
// composed component matches what `wasmtime serve` links.
func minimalHttpProxyWorld(t *testing.T, dir string) *componenttype.World {
	t.Helper()
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	run := func(name string, args ...string) {
		t.Helper()
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	witDir := filepath.Join(dir, "wit")
	if err := os.MkdirAll(witDir, 0o755); err != nil {
		t.Fatalf("mkdir witDir: %v", err)
	}
	run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(witDir, "deps"))
	world := `package local:handler@0.1.0;
world handler {
  import wasi:http/types@0.2.0;
  import wasi:io/streams@0.2.0;
  export wasi:http/incoming-handler@0.2.0;
}
`
	if err := os.WriteFile(filepath.Join(witDir, "world.wit"), []byte(world), 0o644); err != nil {
		t.Fatalf("write world.wit: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "empty.wat", "(module)"), "-o", filepath.Join(dir, "empty.wasm"))
	run(wasmtools, "component", "embed", witDir, "-w", "handler", filepath.Join(dir, "empty.wasm"), "-o", filepath.Join(dir, "embedded.wasm"))
	embeddedBytes, err := os.ReadFile(filepath.Join(dir, "embedded.wasm"))
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, embeddedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}
	return w
}

// TestExportWasiHttpHandlerSetResponseComposes is the next step toward a running
// handler (docs/WIT-BRING-YOUR-OWN.md): a bring-your-own wasi:http handler that
// actually CALLS response-outparam.set to hand back a 200 response, composed
// against the real wasi:http WIT. Proves the gMem trampoline lowers the
// 9-flattened-value set import. (A wasmtime-serve run gate follows once this
// composes.)
func TestExportWasiHttpHandlerSetResponseComposes(t *testing.T) {
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	w := minimalHttpProxyWorld(t, dir)
	mainPath := filepath.Join(dir, "handler.fern")
	if err := os.WriteFile(mainPath, []byte(httpHandlerSetResponseProg), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	info, p := loadCheckMono(t, mainPath)
	core, err := wasmbin.BuildWithOptions(p, info, wasmbin.BuildOptions{ForceMemorySection: true, Preview2WASI: true})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	if !bytes.Contains(core, []byte("[static]response-outparam.set")) {
		t.Fatalf("core missing the set import")
	}
	comp, err := component.ComposeExportsFromWorld(core, w)
	if err != nil {
		t.Fatalf("ComposeExportsFromWorld (handler calling set): %v", err)
	}
	out := filepath.Join(dir, "handler.component.wasm")
	if err := os.WriteFile(out, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if o, err := exec.Command(wasmtools, "validate", out).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, o)
	}
	wit, err := exec.Command(wasmtools, "component", "wit", out).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools component wit: %v\n%s", err, wit)
	}
	if !bytes.Contains(wit, []byte("wasi:http/incoming-handler@0.2.0")) {
		t.Fatalf("component WIT missing the exported wasi:http/incoming-handler:\n%s", wit)
	}
}

// TestExportWasiHttpHandlerServes is the running-server capstone
// (docs/WIT-BRING-YOUR-OWN.md): the bring-your-own wasi:http handler composed
// above is served by `wasmtime serve` and answers a real HTTP request with the
// 200 it set via response-outparam.set — end-to-end proof that a Fern program
// implements wasi:http/incoming-handler from a user-supplied WIT, no embedded
// HTTP world. This is the payoff of P6 Slice 6.
func TestExportWasiHttpHandlerServes(t *testing.T) {
	if got := serveHttpHandlerStatus(t, httpHandlerSetResponseProg); got != http.StatusOK {
		t.Fatalf("status = %d; want 200", got)
	}
}

// buildHttpHandlerComponent compiles a bring-your-own wasi:http handler program
// and composes it against the minimal proxy world, returning the component path.
func buildHttpHandlerComponent(t *testing.T, dir, prog string) string {
	t.Helper()
	w := minimalHttpProxyWorld(t, dir)
	mainPath := filepath.Join(dir, "handler.fern")
	if err := os.WriteFile(mainPath, []byte(prog), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	info, p := loadCheckMono(t, mainPath)
	core, err := wasmbin.BuildWithOptions(p, info, wasmbin.BuildOptions{ForceMemorySection: true, Preview2WASI: true})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	comp, err := component.ComposeExportsFromWorld(core, w)
	if err != nil {
		t.Fatalf("ComposeExportsFromWorld: %v", err)
	}
	out := filepath.Join(dir, "handler.component.wasm")
	if err := os.WriteFile(out, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	return out
}

// serveHttpHandlerStatus builds + composes `prog`, serves it under `wasmtime
// serve`, makes a single `GET /`, and returns the response status.
func serveHttpHandlerStatus(t *testing.T, prog string) int {
	t.Helper()
	status, _ := serveHttpHandler(t, prog)
	return status
}

// serveHttpHandlerBody is serveHttpHandlerStatus but returns the response body.
func serveHttpHandlerBody(t *testing.T, prog string) string {
	t.Helper()
	_, body := serveHttpHandler(t, prog)
	return body
}

// serveHttpHandler builds + composes `prog`, serves the component under
// `wasmtime serve` on a free port, makes a single `GET /`, and returns the
// response status + body. Skips if wasmtime is absent.
func serveHttpHandler(t *testing.T, prog string) (int, string) {
	t.Helper()
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	out := buildHttpHandlerComponent(t, dir, prog)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	srv := exec.Command(wasmtime, "serve", "--addr", addr, out)
	var slog bytes.Buffer
	srv.Stdout = &slog
	srv.Stderr = &slog
	if err := srv.Start(); err != nil {
		t.Fatalf("start wasmtime serve: %v", err)
	}
	defer func() {
		_ = srv.Process.Kill()
		_, _ = srv.Process.Wait()
	}()

	url := fmt.Sprintf("http://%s/", addr)
	var resp *http.Response
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = http.Get(url)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET %s never succeeded: %v\nserver log:\n%s", url, err, slog.String())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}
