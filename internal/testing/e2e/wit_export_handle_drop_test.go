package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

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

	prog := `@import("local:test/res@0.1.0", "thing")
resource Thing;

@import("local:test/res@0.1.0", "[constructor]thing")
function new_thing(): own Thing;

@export("local:test/handler@0.1.0", "handle")
function on_request(): void { let t: own Thing = new_thing(); return; }`
	mainPath := filepath.Join(dir, "reactor.fern")
	if err := os.WriteFile(mainPath, []byte(prog), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	core := e2eharness.SelfHostReactorCore(t, mainPath)
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
