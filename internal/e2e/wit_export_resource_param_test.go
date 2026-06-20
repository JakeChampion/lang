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

// TestExportResourceHandleParamComposes is the P6 Slice 6 entry gate
// (docs/WIT-BRING-YOUR-OWN.md): the composer lifts an `@export` whose parameter
// is a handle (`borrow<R>`) to an imported resource — the shape
// `wasi:http`'s `incoming-handler#handle` needs (`own<incoming-request>`). A
// Fern reactor `@export handle(t: borrow Thing): u32` over an `@import`ed
// `resource Thing` composes: the composer surfaces the imported `thing`
// resource and references it from the export's `borrow<thing>` functype. Gated
// by `wasm-tools validate` + the component WIT declaring the handle param
// (running it needs a resource-provider harness — a later slice, as the scalar
// export slice first shipped validate-only).
func TestExportResourceHandleParamComposes(t *testing.T) {
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

	// A world that IMPORTS a resource interface and EXPORTS a handler taking a
	// borrow of that resource.
	witDir := filepath.Join(dir, "wit")
	if err := os.MkdirAll(witDir, 0o755); err != nil {
		t.Fatalf("mkdir wit: %v", err)
	}
	src := `package local:test@0.1.0;
interface res { resource thing; }
interface handler {
  use res.{thing};
  handle: func(t: borrow<thing>) -> u32;
}
world m {
  import res;
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

	// A reactor implementing handle. `borrow Thing` is the handle vocabulary
	// (P5); it erases to the i32 handle, so the core func is `(i32) -> i32`.
	// The Fern function is NOT named `handle` (that name triggers the checker's
	// handler-main synthesis → tcp_serve); the @export WIT name is "handle".
	prog := `@import("local:test/res@0.1.0", "thing")
resource Thing;

@export("local:test/handler@0.1.0", "handle")
function on_request(t: borrow Thing): u32 { return 42 as u32; }`
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
		t.Fatalf("ComposeExportsFromWorld (resource handle param): %v", err)
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
	if !bytes.Contains(wit, []byte("borrow<thing>")) && !bytes.Contains(wit, []byte("borrow<\n")) {
		t.Fatalf("component WIT missing the borrow<thing> handle param:\n%s", wit)
	}
}
