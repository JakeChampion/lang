package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/encode"
)

// TestWasmP3AsyncImportTupleParamFromFern brings the async `@import` parameter
// surface to parity with the sync one: an async import whose parameter is a
// COMPOSITE (here a `(i32, i32)` tuple, the same machinery as a record/struct).
// A real Fern program —
//
//	@import("test:dep/d","add") async function add(p: (i32, i32)): i32;
//	async function run(): i32 { var p: (i32, i32) = (10, 32); return add(p); }
//
// — flattens the tuple to its canonical element args `(x, y)` via the shared
// sync/async marshalling head (emitExternParamMarshal), runs the `canon lower
// async` call `(x, y, retptr) -> status`, awaits, and reads the scalar result.
// The bundled provider (BuildAsyncLiftedExportComponentTupleParam over the
// param-summing `add` core) receives `p: tuple<u32, u32>` and task-returns x+y.
// Running `run()` under wasmtime's async features returns 42 (10+32) — a tuple
// argument flows colorlessly into an awaited import, so every parameter shape a
// sync `@import` accepts now works async too. See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func TestWasmP3AsyncImportTupleParamFromFern(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	src := `@import("test:dep/d", "add") async function add(p: (i32, i32)): i32;
async function run(): i32 {
	var p: (i32, i32) = (10, 32);
	return add(p);
}
function main(): i32 { return 0; }
`
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		Preview2WASI:    true,
		AsyncExportName: "__async_run",
		AsyncSourceFunc: "run",
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}

	// Provider: `add: async func(p: tuple<u32, u32>) -> u32` returning x+y. The
	// core (p3AsyncAddCoreModule) receives the tuple flattened to (x, y).
	provider := component.BuildAsyncLiftedExportComponentTupleParam(
		p3AsyncAddCoreModule, "add", "add",
		[]byte{component.CValtypeU32, component.CValtypeU32}, component.CValtypeU32,
	)

	i32 := encode.ValtypeI32
	comp := component.BuildAsyncImportsAwaitComponent(core, []component.AsyncImportSpec{{
		Iface: "test:dep/d", WITName: "add",
		Provider:           provider,
		ProviderExportName: "add",
		LowerParams:        []byte{i32, i32, i32}, // (x, y, retptr) — tuple flattened
		LowerResults:       []byte{i32},           // status
		NeedsRealloc:       false,
		// add: async func(p: tuple<u32, u32>) -> u32. The tuple is defined
		// type 0; the param references it by index (sleb 0).
		ImportDefinedTypes: [][]byte{component.InnerTypeTuple([]byte{component.CValtypeU32, component.CValtypeU32})},
		ImportParamNames:   []string{"p"},
		ImportParamVals:    [][]byte{{0x00}},
	}}, "__async_run", "run", component.CValtypeU32)

	p := filepath.Join(dir, "fern_async_tupleparam.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async tuple param import): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("Fern async tuple param import: got %q, want 42 (10+32)", bytes.TrimSpace(out))
	}
}
