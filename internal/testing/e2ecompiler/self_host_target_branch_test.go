package e2ecompiler

import (
	"os/exec"
	"testing"
)

const targetBranchHandlerSrc = `import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (target_os() == "wasi-http") {
        return http.ok("hosted arm");
    } else {
        match (read_file("/etc/hostname")) {
            Ok(text) => { return http.ok("dialled arm " + text); },
            Err(e) => { return http.ok("dialled arm"); }
        }
    }
}
`

const indexingHandlerSrc = `import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let bytes: u8[] = req.path.bytes();
    return http.ok("byte " + (bytes[bytes.len() - 1] as i32).to_string());
}
`

func TestSelfHostWasiHttpTargetBranchGate(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	component := compileWasiHttp(t, dir, targetBranchHandlerSrc, "branch.wasm")
	status, _, body := serveComponent(t, wasmtime, component, "GET", "/", "")
	if status != 200 || body != "hosted arm" {
		t.Fatalf("GET / = %d %q; want 200 \"hosted arm\"", status, body)
	}
}

func TestSelfHostWasiHttpCoreTrapsOnExit(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	component := compileWasiHttp(t, dir, indexingHandlerSrc, "index.wasm")
	status, _, body := serveComponent(t, wasmtime, component, "GET", "/ab", "")
	if status != 200 || body != "byte 98" {
		t.Fatalf("GET /ab = %d %q; want 200 \"byte 98\"", status, body)
	}
}
