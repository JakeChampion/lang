package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/codegen/wasmbin"
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
	buf = component.PutCanonTaskReturnSingle(buf, component.CValtypeU32)                                  // core func 0: task.return
	buf = component.PutCoreModuleSection(buf, p3AsyncCoreModule)                                          // core module 0
	buf = component.PutCoreInstanceSectionFromOneFuncExport(buf, "task-return", 0)                        // core instance 0: provides task-return
	buf = component.PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, []string{""}, []uint32{0}) // core instance 1: the user module
	buf = component.PutAliasSectionCoreExportFunc(buf, 1, "run")                                          // core func 1: the export
	buf = component.PutTypeSectionOneFunc(buf, nil, nil, component.CValtypeU32)                           // type 0: () -> u32
	buf = component.PutCanonSectionLiftAsync(buf, 1, 0)                                                   // component func 0: lift async
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

// TestWasmP3AsyncExportFromFern is the full WASI Preview-3 vertical: a
// real Fern `function main(): i32` is compiled with
// BuildOptions.AsyncExportName, which emits an async core-func shape
// (call main, hand its i32 to the ("","task-return") import, return
// void); the composer then lifts that export with the `async`
// canonical option (BuildAsyncLiftedExportComponent). The resulting
// `run: async func() -> u32` export returns main's value under
// wasmtime's async features — Fern source → runnable async component.
func TestWasmP3AsyncExportFromFern(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	src := "function main(): i32 { return 7 * 6; }\n"
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
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	comp := component.BuildAsyncLiftedExportComponent(core, "__async_run", "run", component.CValtypeU32)

	p := filepath.Join(dir, "fern_async.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("Fern async export: got %q, want 42", bytes.TrimSpace(out))
	}
}
