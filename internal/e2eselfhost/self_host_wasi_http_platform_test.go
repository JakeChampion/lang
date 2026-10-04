package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The capabilities the wasi:http/proxy world grants a handler's bag, on the
// self-host: `plat.log` reaches wasmtime's stderr through wasi:cli/stderr,
// and the wall clock, the monotonic clock and entropy answer through
// wasi:clocks and wasi:random. The body names whichever one did not answer.
// `plat.env` is not granted, so a handler reading it is refused at check time
// (E066, naming the builtin) rather than composed into a component that fails
// to link.
func TestSelfHostWasiHttpPlatformCapabilities(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	component := compileWasiHttp(t, dir, `import "std/http";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    plat.log(f"LOGLINE {req.method} {req.path}");
    if (plat.now_ms() < 1600000000000) {
        return http.text(500, "clock");
    }
    if (plat.elapsed_ns() < 0) {
        return http.text(500, "monotonic");
    }
    if (plat.random_i32() == 0 && plat.random_i32() == 0 && plat.random_i32() == 0) {
        return http.text(500, "random");
    }
    return http.ok("caps-ok");
}
`, "caps.wasm")
	wit, err := exec.Command(wasmtools, "component", "wit", component).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools component wit: %v\n%s", err, wit)
	}
	for _, iface := range []string{"wasi:cli/stderr", "wasi:clocks/wall-clock", "wasi:clocks/monotonic-clock", "wasi:random/random"} {
		if !strings.Contains(string(wit), iface) {
			t.Errorf("component does not import %s:\n%s", iface, wit)
		}
	}
	status, _, body, log := serveComponentLogged(t, wasmtime, component, "GET", "/caps", "", nil, "LOGLINE GET /caps")
	if status != 200 || body != "caps-ok" {
		t.Errorf("GET /caps = %d %q; want 200 \"caps-ok\"", status, body)
	}
	if !strings.Contains(log, "LOGLINE GET /caps") {
		t.Errorf("plat.log line missing from wasmtime serve's stderr:\n%s", log)
	}

	cli, stdlib := witSelfHostCLI(t)
	prog := filepath.Join(dir, "env.fern")
	if err := os.WriteFile(prog, []byte(`import "std/http";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    match (plat.env("X")) { Some(_) => {}, None => {} }
    return http.ok("e");
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(cli, "-target", "wasm32-wasi-http", "-o", filepath.Join(dir, "env.wasm"), prog, stdlib).CombinedOutput()
	if err == nil {
		t.Fatal("a handler reading plat.env compiled for the proxy world, which grants no environment")
	}
	if !strings.Contains(string(out), "E066") || !strings.Contains(string(out), "`env`") {
		t.Errorf("want an E066 refusal naming `env`, got:\n%s", out)
	}
}
