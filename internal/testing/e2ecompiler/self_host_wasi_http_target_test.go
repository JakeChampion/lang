package e2ecompiler

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// wasiHttpRouterSrc is the handler the wasm32-wasi-http gates serve: it
// routes on path and method, reads the body, reads a request header, and
// sets response headers — every part of the request and response the entry
// marshals through the host. std/serve is what a synthesised main
// (flatten.with_handler_main) needs to build the same program.
const wasiHttpRouterSrc = `
import "std/http";
import "std/headers";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/hello") {
        return http.ok("world")
            .with_header("x-served-by", "fern")
            .with_header("content-type", "text/plain");
    }
    if (req.path == "/headers") {
        match (req.headers.get("x-token")) {
            Some(v) => { return http.ok("token=" + v); },
            None => { return http.text(400, "no token"); }
        }
    }
    if (req.method == "POST") {
        match (req.body_string()) {
            Ok(text) => { return http.ok(req.method + ":" + text); },
            Err(e) => { return e.to_response(); }
        }
    }
    return http.text(404, "not found");
}
`

// wasiHttpRequest is one request of the gate, with the response it expects.
type wasiHttpRequest struct {
	method, path, body string
	header             [2]string
	status             int
	want               string
	wantHeader         [2]string
}

var wasiHttpRequests = []wasiHttpRequest{
	{method: "GET", path: "/hello", status: 200, want: "world", wantHeader: [2]string{"x-served-by", "fern"}},
	{method: "GET", path: "/hello", status: 200, want: "world", wantHeader: [2]string{"content-type", "text/plain"}},
	{method: "GET", path: "/missing", status: 404, want: "not found"},
	{method: "POST", path: "/echo", body: "echo me back", status: 200, want: "POST:echo me back"},
	{method: "GET", path: "/headers", header: [2]string{"x-token", "s3cret"}, status: 200, want: "token=s3cret"},
	{method: "GET", path: "/headers", status: 400, want: "no token"},
	// The serve loop's body cap (std/http's http_limits().body, 1 MiB): a
	// body exactly at it reaches the handler, one past it is refused with
	// 413 before the handler runs, as the socket server refuses it (#11102).
	{method: "POST", path: "/echo", body: strings.Repeat("a", 1<<20), status: 200, want: "POST:" + strings.Repeat("a", 1<<20)},
	{method: "POST", path: "/echo", body: strings.Repeat("b", 2<<20), status: 413, want: ""},
}

// compileWasiHttp compiles src with the self-host CLI for wasm32-wasi-http
// (a component, or another -emit form) and returns the output path.
func compileWasiHttp(t *testing.T, dir, src, out string, emit ...string) string {
	t.Helper()
	cli, stdlib := witSelfHostCLI(t)
	prog := filepath.Join(dir, "handler.fern")
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, out)
	args := append([]string{"-target", "wasm32-wasi-http"}, emit...)
	args = append(args, "-o", outPath, prog, stdlib)
	if msg, err := exec.Command(cli, args...).CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI %v: %v\n%s", args, err, msg)
	}
	return outPath
}

func checkWasiHttpRequests(t *testing.T, wasmtime, component string) {
	t.Helper()
	for _, r := range wasiHttpRequests {
		status, hdr, body := serveComponentWithHeader(t, wasmtime, component, r)
		if status != r.status || body != r.want {
			t.Errorf("%s %s = %d %q; want %d %q", r.method, r.path, status, body, r.status, r.want)
		}
		if r.wantHeader[0] != "" && hdr.Get(r.wantHeader[0]) != r.wantHeader[1] {
			t.Errorf("%s %s: header %s = %q; want %q", r.method, r.path, r.wantHeader[0], hdr.Get(r.wantHeader[0]), r.wantHeader[1])
		}
	}
}

// serveComponentWithHeader is serveComponent with the request's one header.
func serveComponentWithHeader(t *testing.T, wasmtime, component string, r wasiHttpRequest) (int, http.Header, string) {
	t.Helper()
	if r.header[0] == "" {
		return serveComponent(t, wasmtime, component, r.method, r.path, r.body)
	}
	return serveComponentHeaders(t, wasmtime, component, r.method, r.path, r.body, map[string]string{r.header[0]: r.header[1]})
}

// TestSelfHostWasiHttpTargetServes is the gate for `-target
// wasm32-wasi-http` on the self-host: the CLI compiles a handler to a
// wasi:http/incoming-handler component that validates under wasm-tools and,
// under `wasmtime serve`, routes on path and method, echoes a body, reads a
// request header and sets response headers.
func TestSelfHostWasiHttpTargetServes(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	component := compileWasiHttp(t, dir, wasiHttpRouterSrc, "handler.wasm")
	if out, err := exec.Command(wasmtools, "validate", component).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	checkWasiHttpRequests(t, wasmtime, component)
}

// TestSelfHostWasiHttpTargetMatchesNative is the differential against the
// component `fern -target wasm32-wasi-http` builds (#6636): the same handler,
// compiled by each route, answers every request of the gate the same — status,
// body and the headers the handler sets.
func TestSelfHostWasiHttpTargetMatchesNative(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	mine := compileWasiHttp(t, dir, wasiHttpRouterSrc, "selfhost.wasm")
	native := buildLangBinForInterp(t)
	theirs := filepath.Join(dir, "native.wasm")
	if out, err := exec.Command(native, "-target", "wasm32-wasi-http", "-o", theirs, filepath.Join(dir, "handler.fern")).CombinedOutput(); err != nil {
		t.Fatalf("native fern -target wasm32-wasi-http: %v\n%s", err, out)
	}
	for _, r := range wasiHttpRequests {
		s1, h1, b1 := serveComponentWithHeader(t, wasmtime, mine, r)
		s2, h2, b2 := serveComponentWithHeader(t, wasmtime, theirs, r)
		if s1 != s2 || b1 != b2 {
			t.Errorf("%s %s: self-host %d %q, native %d %q", r.method, r.path, s1, b1, s2, b2)
		}
		for _, name := range []string{"x-served-by", "content-type"} {
			if h1.Get(name) != h2.Get(name) {
				t.Errorf("%s %s: header %s: self-host %q, native %q", r.method, r.path, name, h1.Get(name), h2.Get(name))
			}
		}
	}
}

// TestSelfHostWasiHttpTargetCoreModule pins the `-emit core-module` form the
// playground's browser host instantiates: a core module exporting the
// handler under its WIT name, its memory and cabi_realloc, and importing
// only from the two interfaces the host implements.
func TestSelfHostWasiHttpTargetCoreModule(t *testing.T) {
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	core := compileWasiHttp(t, dir, wasiHttpRouterSrc, "core.wasm", "-emit", "core-module")
	out, err := exec.Command(wasmtools, "print", core).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print: %v\n%s", err, out)
	}
	text := string(out)
	for _, want := range []string{`(export "wasi:http/incoming-handler@0.2.0#handle"`, `(export "memory"`, `(export "cabi_realloc"`} {
		if !strings.Contains(text, want) {
			t.Errorf("core module lacks %s", want)
		}
	}
	// Every import must be a function the browser host provides: the
	// shim's two tables are the whole of what web/wasi-http-shim.js links,
	// and an import outside them is a LinkError in the page.
	shim := browserHttpShimImports(t)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "(import ") {
			continue
		}
		fields := strings.SplitN(line, `"`, 5)
		if len(fields) < 5 {
			t.Errorf("unreadable import line: %s", line)
			continue
		}
		iface, name := fields[1], fields[3]
		if !shim[iface][name] {
			t.Errorf("core module imports %q from %q, which the browser host does not provide", name, iface)
		}
	}
}

// browserHttpShimImports reads the function names web/wasi-http-shim.js
// provides under each interface, the two tables it instantiates the core
// against, so the test's notion of the host is the file the page loads.
func browserHttpShimImports(t *testing.T) map[string]map[string]bool {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "wasi-http-shim.js"))
	if err != nil {
		t.Fatal(err)
	}
	tables := map[string]string{"wasi:http/types@0.2.0": "httpTypes", "wasi:io/streams@0.2.0": "ioStreams"}
	out := map[string]map[string]bool{}
	for iface, object := range tables {
		start := strings.Index(string(src), "const "+object+" = {")
		if start < 0 {
			t.Fatalf("web/wasi-http-shim.js has no %s table", object)
		}
		body := string(src[start:])
		if end := strings.Index(body, "\n  };"); end >= 0 {
			body = body[:end]
		}
		out[iface] = map[string]bool{}
		for _, m := range regexp.MustCompile(`(?m)^\s{4}"([^"]+)":`).FindAllStringSubmatch(body, -1) {
			out[iface][m[1]] = true
		}
		if len(out[iface]) == 0 {
			t.Fatalf("web/wasi-http-shim.js's %s table has no entries", object)
		}
	}
	return out
}

// TestSelfHostWasiHttpTargetNeedsHandle: a program with no `handle` is
// refused by name, not by an undefined-name error at a line past its end.
func TestSelfHostWasiHttpTargetNeedsHandle(t *testing.T) {
	cli, stdlib := witSelfHostCLI(t)
	dir := t.TempDir()
	prog := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(prog, []byte("function main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(cli, "-target", "wasm32-wasi-http", "-o", filepath.Join(dir, "x.wasm"), prog, stdlib).CombinedOutput()
	if err == nil {
		t.Fatal("a handler-less program compiled for wasm32-wasi-http")
	}
	if !strings.Contains(string(out), "declares no `function handle(") {
		t.Fatalf("refusal does not name the missing handle:\n%s", out)
	}
}
