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
	bin, err := CompileComponent(src, "wasm")
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
	src := `function handle(req: HttpRequest, plat: Platform): HttpResponse {
  return http_response_ok("ok");
}`
	bin, err := CompileComponent(src, "wasi-http")
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
	_, err := CompileComponent(`function main(): i32 { return `, "wasm")
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
	bin, err := CompileComponent(src, "wasm")
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
