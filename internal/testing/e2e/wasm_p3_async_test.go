package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
)

// p3AsyncCoreModule is a hand-built core module for the minimal async
// export: it imports ("", "task-return") (param i32) and exports "run"
// (func index 1) which pushes 42, calls task-return, and returns void —
// the shape an async-lifted export takes (result delivered via
// task.return; function-return = task done).
var p3AsyncCoreModule = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // magic + version
	0x01, 0x08, 0x02, 0x60, 0x01, 0x7f, 0x00, 0x60, 0x00, 0x00, // types: (i32)->(), ()->()
	0x02, 0x10, 0x01, 0x00, 0x0b, 't', 'a', 's', 'k', '-', 'r', 'e', 't', 'u', 'r', 'n', 0x00, 0x00, // import "" "task-return" func 0
	0x03, 0x02, 0x01, 0x01, // func section: 1 func of type 1
	0x07, 0x07, 0x01, 0x03, 'r', 'u', 'n', 0x00, 0x01, // export "run" func 1
	0x0a, 0x08, 0x01, 0x06, 0x00, 0x41, 0x2a, 0x10, 0x00, 0x0b, // code: i32.const 42, call 0, end
}

// TestWasmP3AsyncExportAssembly proves the WASI Preview-3
// component-model-async path end to end: the composer's canonical-async
// emitters (PutCanonSectionLiftAsync + PutCanonTaskReturnSingle) compose
// a core module into a component whose `run: async func() -> u32` export
// returns 42 under wasmtime's async features. This is the runnable
// counterpart to the byte-pinning tests in internal/wasm/component, and
// the assembly the future BuildAsyncLiftedExportComponent will perform.
// See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func TestWasmP3AsyncExportAssembly(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	buf := component.PutComponentHeader(nil)
	buf = component.PutCanonTaskReturnSingle(buf, component.CValtypeU32)                                 // core func 0: task.return
	buf = component.PutCoreModuleSection(buf, p3AsyncCoreModule)                                         // core module 0
	buf = component.PutCoreInstanceSectionFromOneFuncExport(buf, "task-return", 0)                       // core instance 0: provides task-return
	buf = component.PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, []string{""}, []uint32{0}) // core instance 1: the user module
	buf = component.PutAliasSectionCoreExportFunc(buf, 1, "run")                                         // core func 1: the export
	buf = component.PutTypeSectionOneFuncAsync(buf, nil, nil, component.CValtypeU32)                     // type 0: () -> u32
	buf = component.PutCanonSectionLiftAsync(buf, 1, 0)                                                  // component func 0: lift async
	buf = component.PutExportSectionOneFunc(buf, "run", 0)

	dir := t.TempDir()
	p := filepath.Join(dir, "p3async.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("async export: got %q, want 42", bytes.TrimSpace(out))
	}
}

// TestWasmP3NestedComponentReExport exercises the nested-component
// encoders (PutComponentSection + PutInstanceSectionInstantiateComponent)
// — the building block the async-import / await side needs to bundle a
// provider inside a consumer. It embeds the async-export provider as a
// nested component, instantiates it, aliases its `dep: async func() ->
// u32` export, and re-exports it at the outer level. Running the outer
// `dep()` under wasmtime's async features returns the nested provider's
// value (42) — proving nested embedding composes and an async export
// survives nesting + re-export.
func TestWasmP3NestedComponentReExport(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	provider := component.BuildAsyncLiftedExportComponent(p3AsyncCoreModule, "run", "dep", component.CValtypeU32)

	buf := component.PutComponentHeader(nil)
	buf = component.PutComponentSection(buf, provider)               // component 0
	buf = component.PutInstanceSectionInstantiateComponent(buf, 0)   // component instance 0
	buf = component.PutAliasSectionInstanceExportFunc(buf, 0, "dep") // component func 0
	buf = component.PutExportSectionOneFunc(buf, "dep", 0)

	dir := t.TempDir()
	p := filepath.Join(dir, "nested.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "dep()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (nested async): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("nested re-exported async dep: got %q, want 42", bytes.TrimSpace(out))
	}
}

// p3AsyncConsumerCore is a hand-built consumer core module for the
// async-import await: it imports ("","task-return") (i32)->(),
// ("","dep-lower") (i32)->(i32), and ("mem","mem") memory; its "run"
// export calls dep-lower(retptr=8) (the lowered async import), drops the
// status (the import completes synchronously, so the result is already
// at mem[8]), loads mem[8], and hands it to task-return.
var p3AsyncConsumerCore = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // magic + version
	// types: 0:(i32)->()  1:(i32)->(i32)  2:()->()
	0x01, 0x0d, 0x03, 0x60, 0x01, 0x7f, 0x00, 0x60, 0x01, 0x7f, 0x01, 0x7f, 0x60, 0x00, 0x00,
	// imports: "" "task-return" func0 ; "" "dep-lower" func1 ; "mem" "mem" memory(min1)
	0x02, 0x28, 0x03,
	0x00, 0x0b, 't', 'a', 's', 'k', '-', 'r', 'e', 't', 'u', 'r', 'n', 0x00, 0x00,
	0x00, 0x09, 'd', 'e', 'p', '-', 'l', 'o', 'w', 'e', 'r', 0x00, 0x01,
	0x03, 'm', 'e', 'm', 0x03, 'm', 'e', 'm', 0x02, 0x00, 0x01,
	// func section: 1 func of type 2
	0x03, 0x02, 0x01, 0x02,
	// export "run" func 2 (after the 2 imported funcs)
	0x07, 0x07, 0x01, 0x03, 'r', 'u', 'n', 0x00, 0x02,
	// code: i32.const 8; call 1 (dep-lower); drop; i32.const 8; i32.load; call 0 (task-return); end
	0x0a, 0x10, 0x01, 0x0e, 0x00, 0x41, 0x08, 0x10, 0x01, 0x1a, 0x41, 0x08, 0x28, 0x02, 0x00, 0x10, 0x00, 0x0b,
}

// p3MemModule is a core module exporting a linear memory "mem" — the
// shared memory the async lower writes its return area into (sidesteps
// the lower-memory circularity; the real composer reuses its
// memory-trampoline machinery).
var p3MemModule = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // magic + version
	0x05, 0x03, 0x01, 0x00, 0x01, // memory section: 1 mem, limits min 1
	0x07, 0x07, 0x01, 0x03, 'm', 'e', 'm', 0x02, 0x00, // export "mem" memory 0
}

// TestWasmP3AsyncImportAwait is the WASI Preview-3 async IMPORT / await
// demonstration, assembled entirely through the Go composer (no wac, no
// wasm-tools compose): a consumer bundles the async-export provider as a
// NESTED component, lowers its `dep: async func() -> u32` import with
// canon lower async, calls + awaits it (synchronous completion → result
// read from the shared return area), and re-returns it from its own
// async export `run`. Runs under wasmtime's async features → 42. This
// converts the proven nested-component await spike into a permanent,
// CI-tested artifact, exercising both async-ABI directions (lower + lift)
// through real composer code.
func TestWasmP3AsyncImportAwait(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	provider := component.BuildAsyncLiftedExportComponent(p3AsyncCoreModule, "run", "dep", component.CValtypeU32)

	// v46 requires the consumer→provider call to cross a component-instance
	// boundary (a consumer that lowers a provider bundled in its OWN instance
	// traps "cannot enter component instance"). So the consumer machinery is
	// its own nested component ($C) that IMPORTS "dep0", and the outer wires a
	// sibling provider instance into it.
	inner := component.PutComponentHeader(nil)
	inner = component.PutTypeSectionOneFuncAsync(inner, nil, nil, component.CValtypeU32)   // comp type 0 (import type)
	inner = component.PutComponentImportSectionFuncs(inner, []string{"dep0"}, []uint32{0}) // comp func 0 (dep)
	inner = component.PutCoreModuleSection(inner, p3MemModule)                             // core module 0
	inner = component.PutCoreInstanceSectionInstantiate(inner, 0)                          // core instance 0 (mem)
	inner = component.PutAliasSectionCoreExport(inner, component.CoreSortMemory, 0, "mem") // core memory 0
	inner = component.PutCanonTaskReturnSingle(inner, component.CValtypeU32)               // core func 0 (task.return)
	inner = component.PutCanonSectionLowerAsync(inner, 0, 0)                               // core func 1 (dep-lower): lower comp func 0 over mem 0
	inner = component.PutCoreModuleSection(inner, p3AsyncConsumerCore)                     // core module 1
	inner = component.PutCoreInstanceSectionFromExports(inner, []component.CoreInstanceExport{
		{Name: "task-return", Sort: component.CoreSortFunc, Idx: 0},
		{Name: "dep-lower", Sort: component.CoreSortFunc, Idx: 1},
	}) // core instance 1 (cli)
	inner = component.PutCoreInstanceSectionInstantiateWithInstanceArgs(inner, 1, []string{"", "mem"}, []uint32{1, 0}) // core instance 2 (ci)
	inner = component.PutAliasSectionCoreExportFunc(inner, 2, "run")                                                   // core func 2 (run)
	inner = component.PutTypeSectionOneFuncAsync(inner, nil, nil, component.CValtypeU32)                               // comp type 1 (lift type)
	inner = component.PutCanonSectionLiftAsync(inner, 2, 1)                                                            // comp func 1 (run)
	inner = component.PutExportSectionOneFunc(inner, "run", 1)

	buf := component.PutComponentHeader(nil)
	buf = component.PutComponentSection(buf, provider)                                                        // component 0 (provider)
	buf = component.PutInstanceSectionInstantiateComponent(buf, 0)                                            // component instance 0
	buf = component.PutAliasSectionInstanceExportFunc(buf, 0, "dep")                                          // component func 0 (dep)
	buf = component.PutComponentSection(buf, inner)                                                           // component 1 (consumer)
	buf = component.PutInstanceSectionInstantiateComponentWithFuncArgs(buf, 1, []string{"dep0"}, []uint32{0}) // component instance 1
	buf = component.PutAliasSectionInstanceExportFunc(buf, 1, "run")                                          // component func 1 (run)
	buf = component.PutExportSectionOneFunc(buf, "run", 1)

	dir := t.TempDir()
	p := filepath.Join(dir, "await.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async import await): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("async import await: got %q, want 42", bytes.TrimSpace(out))
	}
}
