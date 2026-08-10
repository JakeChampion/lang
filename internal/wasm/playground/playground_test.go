package playground

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// componentHeader is the 8-byte preamble every Component Model binary
// starts with: "\0asm" + version 0x000d + layer 0x0001. A core module
// uses layer 0x0000, so the layer bytes are what distinguish the two.
var componentHeader = []byte{0x00, 0x61, 0x73, 0x6d, 0x0d, 0x00, 0x01, 0x00}

func TestCompileComponentCliRunStructure(t *testing.T) {
	src := `function main(): i32 {
  print("hello from a component");
  return 0;
}`
	bin, err := CompileComponent(src, "wasm32-wasi")
	if err != nil {
		t.Fatalf("CompileComponent(wasm): %v", err)
	}
	if !bytes.HasPrefix(bin, componentHeader) {
		t.Fatalf("output is not a component binary: first 8 bytes = % x", bin[:min(8, len(bin))])
	}
}

func TestCompileComponentHttpHandlerStructure(t *testing.T) {
	// A minimal wasi:http/incoming-handler. The handler signature is
	// what -target wasi-http expects; the body just echoes a fixed
	// 200 response.
	src := `
import "std/http";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
  return http.http_response_ok("ok");
}`
	bin, err := CompileComponent(src, "wasm32-wasi-http")
	if err != nil {
		// The handler surface (HttpRequest / HttpResponse / Platform /
		// http_response_ok) is prelude-provided; if the names drift this
		// test should fail loudly rather than silently skip.
		t.Fatalf("CompileComponent(wasi-http): %v", err)
	}
	if !bytes.HasPrefix(bin, componentHeader) {
		t.Fatalf("output is not a component binary: first 8 bytes = % x", bin[:min(8, len(bin))])
	}
}

func TestCompileComponentUnknownWorld(t *testing.T) {
	if _, err := CompileComponent(`function main(): i32 { return 0; }`, "nope"); err == nil {
		t.Fatal("expected an error for an unknown world")
	}
}

func TestCompileComponentParseErrorFormatted(t *testing.T) {
	_, err := CompileComponent(`function main(): i32 { return `, "wasm32-wasi")
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if !strings.Contains(err.Error(), "<playground>") {
		t.Fatalf("parse error should be diag-formatted, got: %v", err)
	}
}

// TestCompileComponentRunsUnderWasmtime is the end-to-end check: the
// cli/run component this package produces actually executes and prints
// what the program wrote to stdout. Skips when wasmtime isn't on PATH
// so `go test ./...` stays green on a bare developer machine (matching
// the convention in internal/e2e/wasm_preview2_test.go).
func TestCompileComponentRunsUnderWasmtime(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping component execution check")
	}
	const want = "hello from a component"
	src := `function main(): i32 {
  print("` + want + `");
  return 0;
}`
	bin, err := CompileComponent(src, "wasm32-wasi")
	if err != nil {
		t.Fatalf("CompileComponent(wasm): %v", err)
	}
	path := filepath.Join(t.TempDir(), "prog.wasm")
	if err := os.WriteFile(path, bin, 0o644); err != nil {
		t.Fatal(err)
	}
	var sout, serr bytes.Buffer
	run := exec.Command(wasmtime, "run", path)
	run.Stdout = &sout
	run.Stderr = &serr
	if err := run.Run(); err != nil {
		t.Fatalf("wasmtime run: %v\nstdout:\n%s\nstderr:\n%s", err, sout.String(), serr.String())
	}
	if got := strings.TrimSpace(sout.String()); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

// coreHeader is the 8-byte preamble of a Component Model *core*
// module: "\0asm" + version 0x0001 + layer 0x0000. The layer bytes
// (offset 6..7) are 0x0000 for a core module vs 0x0001 for a
// component — the distinguishing marker from componentHeader above.
var coreHeader = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

func TestCompileCoreWasmStructure(t *testing.T) {
	src := `function main(): i32 {
  print("hi");
  return 0;
}`
	bin, err := CompileCoreWasm(src)
	if err != nil {
		t.Fatalf("CompileCoreWasm: %v", err)
	}
	if !bytes.HasPrefix(bin, coreHeader) {
		t.Fatalf("output is not a core module: first 8 bytes = % x", bin[:min(8, len(bin))])
	}
}

func TestCompileCoreWasmParseErrorFormatted(t *testing.T) {
	_, err := CompileCoreWasm(`function main(): i32 { return `)
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if !strings.Contains(err.Error(), "<playground>") {
		t.Fatalf("parse error should be diag-formatted, got: %v", err)
	}
}

// TestCompileCoreWasmRunsUnderWasmtime is the end-to-end check for the
// playground's "Run (wasm)" path: the raw preview-1 core module this
// package produces is a valid WASI command (exports `_start` + an
// imported preview-1 host) that prints what the program wrote and
// surfaces an explicit exit() through proc_exit. wasmtime stands in
// for the browser's WebAssembly.instantiate + web/wasi-shim.js here;
// both drive the same `_start` entry against the same imports. Skips
// when wasmtime isn't on PATH (matching the convention above).
func TestCompileCoreWasmRunsUnderWasmtime(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping core-wasm execution check")
	}
	const want = "hello from core wasm"
	src := `function main(): i32 {
  print("` + want + `");
  exit(7);
  return 0;
}`
	bin, err := CompileCoreWasm(src)
	if err != nil {
		t.Fatalf("CompileCoreWasm: %v", err)
	}
	path := filepath.Join(t.TempDir(), "prog.wasm")
	if err := os.WriteFile(path, bin, 0o644); err != nil {
		t.Fatal(err)
	}
	var sout, serr bytes.Buffer
	run := exec.Command(wasmtime, "run", path)
	run.Stdout = &sout
	run.Stderr = &serr
	runErr := run.Run()
	if got := strings.TrimSpace(sout.String()); got != want {
		t.Fatalf("stdout = %q, want %q (stderr: %s)", got, want, serr.String())
	}
	// `exit(7)` lowers to proc_exit(7); wasmtime reports it as the
	// process exit code. The JS shim mirrors this by capturing the
	// proc_exit argument.
	ee, ok := runErr.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected a non-zero exit from exit(7), got err=%v", runErr)
	}
	if ee.ExitCode() != 7 {
		t.Fatalf("exit code = %d, want 7", ee.ExitCode())
	}
}

func TestCompileHttpHandlerCoreStructure(t *testing.T) {
	src := `
import "std/http";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
  return http.http_response_ok("hi");
}`
	bin, err := CompileHttpHandlerCore(src)
	if err != nil {
		t.Fatalf("CompileHttpHandlerCore: %v", err)
	}
	if !bytes.HasPrefix(bin, coreHeader) {
		t.Fatalf("output is not a core module: first 8 bytes = % x", bin[:min(8, len(bin))])
	}
	// The browser host (web/wasi-http-shim.js) calls these exact
	// exports by name, so a rename should fail loudly here.
	for _, want := range []string{
		"wasi:http/incoming-handler@0.2.0#handle",
		"cabi_realloc",
		"memory",
	} {
		if !bytes.Contains(bin, []byte(want)) {
			t.Errorf("core module missing expected export %q", want)
		}
	}
}

func TestCompileHttpHandlerCoreRejectsNonHandler(t *testing.T) {
	// A program without the `handle` signature still compiles to a
	// module, but it won't carry the handler export. Parse errors
	// stay diag-formatted, matching the other compile entry points.
	_, err := CompileHttpHandlerCore(`function handle(req: HttpRequest, plat: Platform): HttpResponse { return `)
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if !strings.Contains(err.Error(), "<playground>") {
		t.Fatalf("parse error should be diag-formatted, got: %v", err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
