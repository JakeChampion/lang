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

// TestExportWasiHttpHandlerCallsConstructorsComposes is the next step toward a
// running wasi:http handler (docs/WIT-BRING-YOUR-OWN.md): the exported
// `incoming-handler#handle` body CALLS `wasi:http/types` resource constructors
// (`[constructor]fields`, `[constructor]outgoing-response`) — `@import` externs
// returning / taking owned handles — and lets the constructed response auto-drop.
// This integrates the P5 import resource-method path INSIDE a P6 resource-handle
// export, composed against the real `wasi:http` WIT: the composer wires the
// constructor imports (handle in/out, no memory) + the owned-handle
// `[resource-drop]` and lifts the void two-handle export. (Producing an actual
// response still needs `response-outparam.set`, whose `result<own<…>,
// error-code>` arg is the remaining marshalling piece.) Compose-gated by
// `wasm-tools validate`.
func TestExportWasiHttpHandlerCallsConstructorsComposes(t *testing.T) {
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

	// The handler constructs an empty `fields`, builds an `outgoing-response`
	// from it (consuming the fields handle), and returns — the response handle is
	// a local owned handle, so it auto-drops. (Not named `handle`: that triggers
	// the checker's tcp_serve handler-main synthesis.)
	prog := `@import("wasi:http/types@0.2.0", "incoming-request")
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

@export("wasi:http/incoming-handler@0.2.0", "handle")
function on_request(request: own IncomingRequest, response_out: own ResponseOutparam): void {
	var headers: own Fields = fields_new();
	var resp: own OutgoingResponse = response_new(headers);
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
	for _, want := range []string{
		"wasi:http/incoming-handler@0.2.0#handle",
		"[constructor]fields",
		"[constructor]outgoing-response",
		"[resource-drop]outgoing-response",
	} {
		if !bytes.Contains(core, []byte(want)) {
			t.Fatalf("core missing %q", want)
		}
	}
	comp, err := component.ComposeExportsFromWorld(core, w)
	if err != nil {
		t.Fatalf("ComposeExportsFromWorld (handler calling wasi:http constructors): %v", err)
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
