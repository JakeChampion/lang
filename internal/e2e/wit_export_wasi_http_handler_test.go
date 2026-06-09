package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestExportWasiHttpIncomingHandlerComposes is the P6 capstone composer gate
// (docs/WIT-BRING-YOUR-OWN.md): a Fern program `@export`s the REAL
// `wasi:http/incoming-handler@0.2.0#handle` — `func(own<incoming-request>,
// own<response-outparam>)` — and the world-driven composer produces a valid
// `wasi:http` component against the actual `wasi:http` WIT (the repo's
// `cmd/fern/wit` `http` world, supplied as input — NOT the compiler's embedded
// HTTP world). This is the headline "bring-your-own-WIT" demonstration: the
// HTTP handler shape composes from a user-supplied `.wit` via `@export` +
// resource-handle params, with no HTTP-specific knowledge in the composer.
// Gated by `wasm-tools validate` + the component WIT exporting
// `wasi:http/incoming-handler`. (A response-producing body + a `wasmtime serve`
// run harness are the follow-on; this proves the compile+compose path.)
func TestExportWasiHttpIncomingHandlerComposes(t *testing.T) {
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()
	run := func(name string, args ...string) {
		t.Helper()
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}

	// Use the repo's real wasi:http WIT (the `http` world exports
	// wasi:http/incoming-handler and imports wasi:http/types + the preview-2 set).
	witDir := filepath.Join(dir, "wit")
	run("cp", "-r", "../../cmd/fern/wit", witDir)
	run(wasmtools, "parse", mustWrite(t, dir, "empty.wat", "(module)"), "-o", filepath.Join(dir, "empty.wasm"))
	run(wasmtools, "component", "embed", witDir, "-w", "http", filepath.Join(dir, "empty.wasm"), "-o", filepath.Join(dir, "embedded.wasm"))
	embeddedBytes, err := os.ReadFile(filepath.Join(dir, "embedded.wasm"))
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, embeddedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}

	// A Fern reactor implementing the handler. The owned request / response-out
	// handles are the canonical incoming-handler#handle params (a bare WIT
	// resource param is `own`). The Fern function is NOT named `handle` (that
	// triggers the checker's tcp_serve handler-main synthesis); the @export WIT
	// name is "handle".
	prog := `@import("wasi:http/types@0.2.0", "incoming-request")
resource IncomingRequest;
@import("wasi:http/types@0.2.0", "response-outparam")
resource ResponseOutparam;

@export("wasi:http/incoming-handler@0.2.0", "handle")
function on_request(request: own IncomingRequest, response_out: own ResponseOutparam): void {
	return;
}`
	mainPath := filepath.Join(dir, "handler.fern")
	if err := os.WriteFile(mainPath, []byte(prog), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	info, p := loadCheckMono(t, mainPath)
	core, err := wasmbin.BuildWithOptions(p, info, wasmbin.BuildOptions{ForceMemorySection: true, Preview2WASI: true})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	if !bytes.Contains(core, []byte("wasi:http/incoming-handler@0.2.0#handle")) {
		t.Fatalf("core missing the surfaced @export core export")
	}
	comp, err := component.ComposeExportsFromWorld(core, w)
	if err != nil {
		t.Fatalf("ComposeExportsFromWorld (wasi:http incoming-handler): %v", err)
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
