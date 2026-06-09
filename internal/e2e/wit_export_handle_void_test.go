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

// TestExportHandleVoidComposes is the P6 Slice 6b gate (docs/WIT-BRING-YOUR-OWN.md):
// the composer lifts a VOID export taking MULTIPLE resource-handle params — the
// shape of `wasi:http`'s `incoming-handler#handle`
// (`func(own<incoming-request>, own<response-outparam>)`, no result). This slice
// proves the void-result + multi-handle-param composer path with `borrow`
// handles (never auto-dropped); the `own`-consume + `[resource-drop]`-in-reactor
// path follows. Gated by `wasm-tools validate` + the WIT declaring the void
// two-handle export.
func TestExportHandleVoidComposes(t *testing.T) {
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
	if err := os.MkdirAll(witDir, 0o755); err != nil {
		t.Fatalf("mkdir wit: %v", err)
	}
	// Two imported resources + a void handler taking a borrow of each — the
	// incoming-handler#handle shape (own→borrow to avoid the auto-drop, which is
	// the next slice).
	src := `package local:test@0.1.0;
interface types { resource req; resource resp; }
interface handler {
  use types.{req, resp};
  handle: func(r: borrow<req>, o: borrow<resp>);
}
world m {
  import types;
  export handler;
}
`
	if err := os.WriteFile(filepath.Join(witDir, "world.wit"), []byte(src), 0o644); err != nil {
		t.Fatalf("write world.wit: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "empty.wat", "(module)"), "-o", filepath.Join(dir, "empty.wasm"))
	run(wasmtools, "component", "embed", witDir, "-w", "m", filepath.Join(dir, "empty.wasm"), "-o", filepath.Join(dir, "embedded.wasm"))
	embeddedBytes, err := os.ReadFile(filepath.Join(dir, "embedded.wasm"))
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, embeddedBytes))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}

	// Reactor: a void handler taking two borrowed handles (not named `handle` —
	// that triggers the checker's handler-main synthesis).
	prog := `@import("local:test/types@0.1.0", "req")
resource Req;
@import("local:test/types@0.1.0", "resp")
resource Resp;

@export("local:test/handler@0.1.0", "handle")
function on_request(r: borrow Req, o: borrow Resp): void { return; }`
	mainPath := filepath.Join(dir, "reactor.fern")
	if err := os.WriteFile(mainPath, []byte(prog), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	info, p := loadCheckMono(t, mainPath)
	core, err := wasmbin.BuildWithOptions(p, info, wasmbin.BuildOptions{ForceMemorySection: true, Preview2WASI: true})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	if !bytes.Contains(core, []byte("local:test/handler@0.1.0#handle")) {
		t.Fatalf("core missing the surfaced @export core export")
	}
	comp, err := component.ComposeExportsFromWorld(core, w)
	if err != nil {
		t.Fatalf("ComposeExportsFromWorld (void two-handle export): %v", err)
	}
	out := filepath.Join(dir, "reactor.component.wasm")
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
	if !bytes.Contains(wit, []byte("local:test/handler@0.1.0")) {
		t.Fatalf("component WIT missing the exported handler interface:\n%s", wit)
	}
	// The exported handle must be void and take both borrowed handles.
	if !bytes.Contains(wit, []byte("borrow<req>")) || !bytes.Contains(wit, []byte("borrow<resp>")) {
		t.Fatalf("component WIT missing the borrow handle params:\n%s", wit)
	}
}
