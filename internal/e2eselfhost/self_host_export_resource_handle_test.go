package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestSelfHostExportResourceHandleComposes is the self-host parity gate for the
// P6 Slice 6 resource-handle export shapes (the Go composer side is
// TestExportResourceHandleParamComposes / …HandleVoid / …OwnedHandleDrop). The
// composer is Go-only (the self-host composer was retired), so the self-host's
// role is emitting a compatible `@export` CORE: a void handler taking a handle
// param (erased to i32) that also holds a locally-constructed owned handle which
// auto-drops (emitting `[resource-drop]`). This confirms the self-hosted
// compiler emits that core and the Go composer lifts it — surfacing the handle
// param's resource + wiring the drop — validated by `wasm-tools`.
func TestSelfHostExportResourceHandleComposes(t *testing.T) {
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	run := func(name string, args ...string) {
		t.Helper()
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}

	copySelfHostDriver(t, dir, "wasm_runio_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_runio_run.fern", "wasm_runio_run")

	// A void handler taking a borrowed handle, which also constructs + drops a
	// local owned handle (exercising handle params + owned-handle auto-drop in a
	// reactor export). Not named `handle` (the checker's handler-main synthesis).
	handlerSrc := `@import("local:test/res@0.1.0", "thing")
resource Thing;

@import("local:test/res@0.1.0", "[constructor]thing")
function new_thing(): own Thing;

@export("local:test/handler@0.1.0", "handle")
function on_request(t: borrow Thing): void {
	var local: own Thing = new_thing();
	return;
}`
	watBytes := runCapture(t, gcc, runner, driverBin, []byte(handlerSrc))
	if !bytes.Contains(watBytes, []byte("local:test/handler@0.1.0#handle")) {
		t.Fatalf("self-host core missing the surfaced @export:\n%s", watBytes)
	}
	if !bytes.Contains(watBytes, []byte("[resource-drop]thing")) {
		t.Fatalf("self-host core missing the auto-inserted [resource-drop]thing:\n%s", watBytes)
	}
	expWatPath := filepath.Join(dir, "exp_core.wat")
	if err := os.WriteFile(expWatPath, watBytes, 0o644); err != nil {
		t.Fatalf("write exporter wat: %v", err)
	}
	expCorePath := filepath.Join(dir, "exp_core.wasm")
	run(wasmtools, "parse", expWatPath, "-o", expCorePath)
	expCore, err := os.ReadFile(expCorePath)
	if err != nil {
		t.Fatalf("read exporter core: %v", err)
	}

	// The world: imports res (resource thing + its constructor), exports the
	// handler taking a borrow of thing.
	expWit := filepath.Join(dir, "expwit")
	if err := os.MkdirAll(expWit, 0o755); err != nil {
		t.Fatalf("mkdir expwit: %v", err)
	}
	// The self-host run-io core also imports wasi:cli/stdout, so the world
	// declares it alongside the custom res/handler interfaces.
	run("cp", "-r", "../../cmd/fern/wit/deps", filepath.Join(expWit, "deps"))
	src := `package local:test@0.1.0;
interface res { resource thing { constructor(); } }
interface handler {
  use res.{thing};
  handle: func(t: borrow<thing>);
}
world m {
  import wasi:cli/stdout@0.2.0;
  import res;
  export handler;
}
`
	if err := os.WriteFile(filepath.Join(expWit, "world.wit"), []byte(src), 0o644); err != nil {
		t.Fatalf("write exporter wit: %v", err)
	}
	run(wasmtools, "parse", mustWrite(t, dir, "ee.wat", "(module)"), "-o", filepath.Join(dir, "ee.wasm"))
	run(wasmtools, "component", "embed", expWit, "-w", "m", filepath.Join(dir, "ee.wasm"), "-o", filepath.Join(dir, "eembed.wasm"))
	expEmbed, err := os.ReadFile(filepath.Join(dir, "eembed.wasm"))
	if err != nil {
		t.Fatalf("read exporter embed: %v", err)
	}
	w, err := componenttype.DecodeWorldBytes(extractComponentType(t, expEmbed))
	if err != nil {
		t.Fatalf("DecodeWorldBytes: %v", err)
	}
	comp, err := component.ComposeExportsFromWorld(expCore, w)
	if err != nil {
		t.Fatalf("ComposeExportsFromWorld (self-host resource-handle export): %v", err)
	}
	out := filepath.Join(dir, "exporter.component.wasm")
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
	if !bytes.Contains(wit, []byte("borrow<thing>")) {
		t.Fatalf("component WIT missing the borrow<thing> handle param:\n%s", wit)
	}
	printed, err := exec.Command(wasmtools, "print", out).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print: %v\n%s", err, printed)
	}
	if !bytes.Contains(printed, []byte("resource.drop")) {
		t.Fatalf("composed component missing the canon resource.drop:\n%s", printed)
	}
}
