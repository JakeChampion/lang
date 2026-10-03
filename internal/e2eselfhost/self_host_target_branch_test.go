package e2eselfhost

import (
	"os/exec"
	"testing"
)

// targetBranchHandlerSrc branches on the target: the wasi-http arm answers
// from the request alone, the other reads a file, which the proxy world
// does not grant. The capability gate judges the arm the target takes, so
// the handler compiles for wasm32-wasi-http and answers the hosted arm.
const targetBranchHandlerSrc = `import "std/http";
import "std/serve";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
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

// indexingHandlerSrc indexes a byte array the request decides, so its
// bounds trap is in the core. A proxy-world core has no wasi:cli/exit to
// end a process with, so the trap is a plain `unreachable` and the
// component still composes against the proxy world.
const indexingHandlerSrc = `import "std/http";
import "std/serve";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    let bytes: u8[] = req.path.bytes();
    return http.ok("byte " + (bytes[bytes.len() - 1] as i32).to_string());
}
`

// TestSelfHostWasiHttpTargetBranchGate: a branch on target_os() is pruned
// before the capability gate (E066) and the shake, so the dead arm's
// `read_file` never reaches either, and the live arm is what serves.
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

// TestSelfHostWasiHttpCoreTrapsOnExit: a handler whose core would exit (the
// bounds trap of an index) composes against the proxy world, which declares
// no wasi:cli/exit, and serves; the in-range request answers.
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
