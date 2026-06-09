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

// TestExportOwnedHandleDropComposes is the P6 Slice 6c gate
// (docs/WIT-BRING-YOUR-OWN.md): an `@export` taking an OWNED handle (`own<R>`)
// the handler doesn't pass on — the auto-drop pass inserts the resource's
// `[resource-drop]`, so the reactor core imports it. `ComposeExportsFromWorld`
// now wires that drop (surfacing the resource + a canon `resource.drop`),
// instead of rejecting it — the last composer gap before the real `wasi:http`
// `incoming-handler#handle` (whose `own<...>` params are consumed/dropped).
// Gated by `wasm-tools validate` + the component declaring `own<thing>` and a
// `resource.drop`.
func TestExportOwnedHandleDropComposes(t *testing.T) {
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
	src := `package local:test@0.1.0;
interface res { resource thing { constructor(); } }
interface handler {
  handle: func();
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

	// The handler creates a local owned handle that isn't passed on, so the
	// auto-drop pass inserts its `[resource-drop]thing` — the core imports it, and
	// the composer must wire it. (Owned *params* are left to the caller; an owned
	// *local* is dropped at scope exit — the P5 contract.)
	prog := `@import("local:test/res@0.1.0", "thing")
resource Thing;

@import("local:test/res@0.1.0", "[constructor]thing")
function new_thing(): own Thing;

@export("local:test/handler@0.1.0", "handle")
function on_request(): void { var t: own Thing = new_thing(); return; }`
	mainPath := filepath.Join(dir, "reactor.fern")
	if err := os.WriteFile(mainPath, []byte(prog), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	info, p := loadCheckMono(t, mainPath)
	core, err := wasmbin.BuildWithOptions(p, info, wasmbin.BuildOptions{ForceMemorySection: true, Preview2WASI: true})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	if !bytes.Contains(core, []byte("[resource-drop]thing")) {
		t.Fatalf("core missing the auto-inserted [resource-drop]thing import")
	}
	comp, err := component.ComposeExportsFromWorld(core, w)
	if err != nil {
		t.Fatalf("ComposeExportsFromWorld (owned-handle drop): %v", err)
	}
	out := filepath.Join(dir, "reactor.component.wasm")
	if err := os.WriteFile(out, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if o, err := exec.Command(wasmtools, "validate", out).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, o)
	}
	printed, err := exec.Command(wasmtools, "print", out).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print: %v\n%s", err, printed)
	}
	if !bytes.Contains(printed, []byte("resource.drop")) {
		t.Fatalf("composed component missing the canon resource.drop:\n%s", printed)
	}
	wit, err := exec.Command(wasmtools, "component", "wit", out).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools component wit: %v\n%s", err, wit)
	}
	if !bytes.Contains(wit, []byte("local:test/handler@0.1.0")) {
		t.Fatalf("component WIT missing the exported handler interface:\n%s", wit)
	}
}
