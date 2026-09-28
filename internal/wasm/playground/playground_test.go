package playground

import (
	"bytes"
	"strings"
	"testing"
)

// componentHeader is the 8-byte preamble every Component Model binary
// starts with: "\0asm" + version 0x000d + layer 0x0001. A core module
// uses layer 0x0000, so the layer bytes are what distinguish the two.
var componentHeader = []byte{0x00, 0x61, 0x73, 0x6d, 0x0d, 0x00, 0x01, 0x00}

// coreHeader is a core module's preamble: "\0asm" + version 1, layer 0.
var coreHeader = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

func TestCompileHttpComponentStructure(t *testing.T) {
	// A minimal wasi:http/incoming-handler. The handler signature is
	// what -target wasi-http expects; the body just echoes a fixed
	// 200 response.
	src := `
import "std/http";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
  return http.http_response_ok("ok");
}`
	bin, err := CompileHttpComponent(src)
	if err != nil {
		// The handler surface (HttpRequest / HttpResponse / Platform /
		// http_response_ok) is prelude-provided; if the names drift this
		// test should fail loudly rather than silently skip.
		t.Fatalf("CompileHttpComponent: %v", err)
	}
	if !bytes.HasPrefix(bin, componentHeader) {
		t.Fatalf("output is not a component binary: first 8 bytes = % x", bin[:min(8, len(bin))])
	}
}

func TestCompileHttpComponentParseErrorFormatted(t *testing.T) {
	_, err := CompileHttpComponent(`function handle(req: HttpRequest, plat: Platform): HttpResponse { return `)
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if !strings.Contains(err.Error(), "<playground>") {
		t.Fatalf("parse error should be diag-formatted, got: %v", err)
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
