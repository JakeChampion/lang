package e2ecompiler

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
)

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
	{method: "POST", path: "/echo", body: strings.Repeat("a", 1<<20), status: 200, want: "POST:" + strings.Repeat("a", 1<<20)},
	{method: "POST", path: "/echo", body: strings.Repeat("b", 2<<20), status: 413, want: ""},
}

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

func serveComponentWithHeader(t *testing.T, wasmtime, component string, r wasiHttpRequest) (int, http.Header, string) {
	t.Helper()
	if r.header[0] == "" {
		return serveComponent(t, wasmtime, component, r.method, r.path, r.body)
	}
	return serveComponentHeaders(t, wasmtime, component, r.method, r.path, r.body, map[string]string{r.header[0]: r.header[1]})
}

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

func browserHttpShimImports(t *testing.T) map[string]map[string]bool {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "wasi-http-shim.js"))
	if err != nil {
		t.Fatal(err)
	}
	tables := map[string]string{
		"wasi:http/types@0.2.0":    "httpTypes",
		"wasi:io/streams@0.2.0":    "ioStreams",
		"wasi:cli/stdout@0.2.0":    "cliStdout",
		"wasi:cli/stderr@0.2.0":    "cliStderr",
		"wasi:io/error@0.2.0":      "ioError",
		"wasi:random/random@0.2.0": "random",
	}
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

func TestSelfHostWasiHttpTargetRefusesStatefulHandler(t *testing.T) {
	cli, stdlib := witSelfHostCLI(t)
	dir := t.TempDir()
	prog := filepath.Join(dir, "main.fern")
	src := `import "std/http";
import "std/platform";

function handle(n: i32, req: HttpRequest, plat: platform.Platform): (i32, HttpResponse) {
    return (n + 1, http.ok("x"));
}
`
	refuses := func(t *testing.T, src, want string) {
		t.Helper()
		if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{
			{"-check", "-target", "wasm32-wasi-http", prog, stdlib},
			{"-target", "wasm32-wasi-http", "-o", filepath.Join(dir, "x.wasm"), prog, stdlib},
		} {
			out, err := exec.Command(cli, args...).CombinedOutput()
			if err == nil {
				t.Errorf("%v: accepted for wasm32-wasi-http", args)
			}
			if string(out) != want {
				t.Errorf("%v:\n got %q\nwant %q", args, out, want)
			}
		}
	}
	refuses(t, src, prog+":4:1: error[E075]: "+checker.WasiHTTPStatefulHandler+"\n")
	refuses(t, `import "std/http";

function handle(req: HttpRequest): HttpResponse {
    return http.ok("x");
}
`, prog+":3:1: error[E075]: a wasi:http handler takes `(req, plat)`; this `handle` takes 1 parameter(s)\n")
}
