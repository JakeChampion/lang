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
	if err := constfold.Fold(prog, nil); err != nil {
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

// TestWasmP3AsyncExportU64FromFern proves the async EXPORT side handles a 64-bit
// result. A real Fern `async function big(): u64 { return 4294967338; }` (2^32 +
// 42) is compiled with AsyncExportName/AsyncSourceFunc and lifted as `big: async
// func() -> u64`: the synthetic wrapper hands the i64 result to a task-return
// import width-matched to i64, not hard-wired to i32. Running it
// returns the full 64-bit value under wasmtime's async features — a value that
// would be truncated if the result were still forced through i32.
func TestWasmP3AsyncExportU64FromFern(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	src := "async function big(): u64 { return 4294967338; }\nfunction main(): i32 { return 0; }\n"
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
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
		AsyncSourceFunc: "big",
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	comp := component.BuildAsyncLiftedExportComponent(core, "__async_run", "big", component.CValtypeU64)

	p := filepath.Join(dir, "fern_async_u64.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "big()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async u64 export): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("4294967338")) {
		t.Errorf("Fern async u64 export: got %q, want 4294967338", bytes.TrimSpace(out))
	}
}

// TestCmdLangAsyncExport drives a Fern program through the actual CLI
// (`fern -target wasm -emit core-module -async-export`) and runs the produced
// component's `run: async func() -> u32` export under wasmtime's async
// features — the user-facing surface for WASI Preview-3 async exports.
func TestCmdLangAsyncExport(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "aexport.fern")
	if err := os.WriteFile(srcPath, []byte("function main(): i32 { return 7 * 6; }\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "aexport.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm", "-emit", "core-module", "-async-export", "-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -async-export failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("CLI async export: got %q, want 42", bytes.TrimSpace(out))
	}
}

// TestCmdLangAsyncFunctionKeyword drives the `async function` keyword
// through the CLI (no -async-export flag): the async-marked function is
// lifted as the component's `<name>: async func() -> u32` export, run
// under wasmtime's async features.
func TestCmdLangAsyncFunctionKeyword(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "akw.fern")
	src := "async function compute(): i32 { return 6 * 7; }\nfunction main(): i32 { return 0; }\n"
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "akw.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm", "-emit", "core-module", "-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern (async function) failed: %v\n%s", err, out)
	}
	// The async-marked function is exported under its own name.
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "compute()", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("async function keyword: got %q, want 42", bytes.TrimSpace(out))
	}
}

// TestCmdLangAsyncFunctionKeywordF64 drives an `async function` returning a
// non-i32 scalar through the CLI: `async function half(): f64 { return 3.5; }`
// is lifted as `half: async func() -> f64` (the CLI derives the component result
// valtype from the source's return type). Running `half()` under wasmtime's
// async features prints 3.5 — proving the async export width plumbing works end
// to end from `fern -target wasm -emit core-module`, not just the i32 default.
func TestCmdLangAsyncFunctionKeywordF64(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "akwf.fern")
	src := "async function half(): f64 { return 3.5; }\nfunction main(): i32 { return 0; }\n"
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	compPath := filepath.Join(dir, "akwf.wasm")
	cmd := exec.Command("go", "run", "./cmd/fern",
		"-target", "wasm", "-emit", "core-module", "-o", compPath, srcPath)
	cmd.Dir = projectRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern (async function f64) failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "half()", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async f64): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("3.5")) {
		t.Errorf("async f64 export: got %q, want 3.5", bytes.TrimSpace(out))
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

// TestWasmP3AsyncImportFromFern is the full colorless async-import vertical from
// REAL Fern source (docs/WASI-PREVIEW3-ASYNC-PLAN.md): a program declares
// `@import(...) async function dep(): i32` and an `async function run()` that
// just calls `dep()` — the await is colorless, no `await` keyword. The wasmbin
// backend lowers `dep` to the `canon lower async` import shape (raw
// `(retptr) -> status` + a colorless wrapper), and BuildAsyncImportAwaitComponent
// composes it against a bundled async provider: it lowers the import `async` over
// the consumer's memory via the trampoline, lifts `run` async, and bundles the
// provider as a nested component. Running `run()` under wasmtime's async
// features returns the provider's value (42) — proving Fern source flows
// colorlessly through both async-ABI directions (lower + lift). This is the
// real-source counterpart to the hand-built-core TestWasmP3AsyncImportAwait.
func TestWasmP3AsyncImportFromFern(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	src := `@import("test:dep/d", "compute") async function dep(): i32;
async function run(): i32 { return dep(); }
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
	if err := constfold.Fold(prog, nil); err != nil {
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

	// The bundled provider supplies `compute: async func() -> u32` returning 42.
	provider := component.BuildAsyncLiftedExportComponent(p3AsyncCoreModule, "run", "compute", component.CValtypeU32)

	// The async-lowered import is `(retptr) -> status` = (i32) -> (i32).
	comp := component.BuildAsyncImportAwaitComponent(
		core, "test:dep/d", "compute",
		provider, "compute",
		"__async_run", "run",
		[]byte{encode.ValtypeI32}, []byte{encode.ValtypeI32},
		component.CValtypeU32,
	)

	p := filepath.Join(dir, "fern_async_import.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async import from Fern): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("Fern async import: got %q, want 42", bytes.TrimSpace(out))
	}
}

// p3AsyncAddCoreModule is a hand-built async-export provider core for an async
// import that takes PARAMETERS: it imports ("", "task-return") (i32)->() and
// exports "add" of type (i32,i32)->() — it sums its two params and delivers the
// result through task-return (function-return = task done), the shape a
// param-taking async-lifted export takes.
var p3AsyncAddCoreModule = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // magic + version
	// types: 0:(i32)->()  1:(i32,i32)->()
	0x01, 0x0a, 0x02, 0x60, 0x01, 0x7f, 0x00, 0x60, 0x02, 0x7f, 0x7f, 0x00,
	// import "" "task-return" func 0 (type 0)
	0x02, 0x10, 0x01, 0x00, 0x0b, 't', 'a', 's', 'k', '-', 'r', 'e', 't', 'u', 'r', 'n', 0x00, 0x00,
	// func section: 1 func of type 1
	0x03, 0x02, 0x01, 0x01,
	// export "add" func 1
	0x07, 0x07, 0x01, 0x03, 'a', 'd', 'd', 0x00, 0x01,
	// code: local.get 0, local.get 1, i32.add, call 0 (task-return), end
	0x0a, 0x0b, 0x01, 0x09, 0x00, 0x20, 0x00, 0x20, 0x01, 0x6a, 0x10, 0x00, 0x0b,
}

// TestWasmP3AsyncImportParamsFromFern extends the colorless async-import vertical
// to an import that takes PARAMETERS: a real Fern `@import(...) async function
// add(a: i32, b: i32): i32` plus a colorless `async function run() { return
// add(40, 2); }`. The wasmbin wrapper forwards the two scalar params to the
// `canon lower async` import `(a, b, retptr) -> status`, and the bundled
// provider (a param-taking async export `add: async func(u32, u32) -> u32`)
// computes a+b and delivers it via task-return. Running `run()` under wasmtime's
// async features returns 42 — proving scalar params flow colorlessly through the
// async lower/lift round-trip. See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func TestWasmP3AsyncImportParamsFromFern(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	src := `@import("test:dep/d", "add") async function add(a: i32, b: i32): i32;
async function run(): i32 { return add(40, 2); }
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
	if err := constfold.Fold(prog, nil); err != nil {
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

	// Provider: `add: async func(a: u32, b: u32) -> u32` returning a+b.
	provider := component.BuildAsyncLiftedExportComponentParams(
		p3AsyncAddCoreModule, "add", "add",
		[]string{"a", "b"}, []byte{component.CValtypeU32, component.CValtypeU32},
		component.CValtypeU32,
	)

	// The async-lowered import is `(a, b, retptr) -> status` = (i32,i32,i32)->(i32).
	// The import's component functype is `add: async func(a: u32, b: u32) -> u32`.
	comp := component.BuildAsyncImportsAwaitComponent(
		core, []component.AsyncImportSpec{{
			Iface: "test:dep/d", WITName: "add",
			Provider: provider, ProviderExportName: "add",
			LowerParams:      []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
			LowerResults:     []byte{encode.ValtypeI32},
			ImportParamNames: []string{"a", "b"},
			ImportParamVals:  [][]byte{{component.CValtypeU32}, {component.CValtypeU32}},
		}},
		"__async_run", "run",
		component.CValtypeU32,
	)

	p := filepath.Join(dir, "fern_async_add.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async import params): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("Fern async import w/ params: got %q, want 42", bytes.TrimSpace(out))
	}
}

// p3AsyncBigCoreModule is a hand-built async-export provider core for an async
// import that returns a 64-bit result: it imports ("", "task-return") (i64)->()
// and exports "big" of type ()->() — it delivers the constant 4294967338
// (2^32 + 42, genuinely > 32 bits) through task-return as an i64. Pairs with a
// `big: async func() -> u64` lift to exercise the wrapper's i64 return-area read.
var p3AsyncBigCoreModule = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // magic + version
	// types: 0:(i64)->()  1:()->()
	0x01, 0x08, 0x02, 0x60, 0x01, 0x7e, 0x00, 0x60, 0x00, 0x00,
	// import "" "task-return" func 0 (type 0)
	0x02, 0x10, 0x01, 0x00, 0x0b, 't', 'a', 's', 'k', '-', 'r', 'e', 't', 'u', 'r', 'n', 0x00, 0x00,
	// func section: 1 func of type 1
	0x03, 0x02, 0x01, 0x01,
	// export "big" func 1
	0x07, 0x07, 0x01, 0x03, 'b', 'i', 'g', 0x00, 0x01,
	// code: i64.const 4294967338, call 0 (task-return), end
	0x0a, 0x0c, 0x01, 0x0a, 0x00, 0x42, 0xaa, 0x80, 0x80, 0x80, 0x10, 0x10, 0x00, 0x0b,
}

// p3AsyncCore40 / p3AsyncCore2 are async-export provider cores returning 40 and
// 2 (an i32.const + task-return + void), used to back two distinct async imports
// the consumer awaits and sums to 42. Structurally identical to
// p3AsyncCoreModule (which returns 42), only the constant differs.
var p3AsyncCore40 = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x08, 0x02, 0x60, 0x01, 0x7f, 0x00, 0x60, 0x00, 0x00,
	0x02, 0x10, 0x01, 0x00, 0x0b, 't', 'a', 's', 'k', '-', 'r', 'e', 't', 'u', 'r', 'n', 0x00, 0x00,
	0x03, 0x02, 0x01, 0x01,
	0x07, 0x07, 0x01, 0x03, 'r', 'u', 'n', 0x00, 0x01,
	0x0a, 0x08, 0x01, 0x06, 0x00, 0x41, 0x28, 0x10, 0x00, 0x0b, // i32.const 40
}

var p3AsyncCore2 = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x08, 0x02, 0x60, 0x01, 0x7f, 0x00, 0x60, 0x00, 0x00,
	0x02, 0x10, 0x01, 0x00, 0x0b, 't', 'a', 's', 'k', '-', 'r', 'e', 't', 'u', 'r', 'n', 0x00, 0x00,
	0x03, 0x02, 0x01, 0x01,
	0x07, 0x07, 0x01, 0x03, 'r', 'u', 'n', 0x00, 0x01,
	0x0a, 0x08, 0x01, 0x06, 0x00, 0x41, 0x02, 0x10, 0x00, 0x0b, // i32.const 2
}

// TestWasmP3AsyncImportMultiFromFern proves the colorless async-import vertical
// generalises to MULTIPLE awaited imports in one program — the edge-handler
// shape that overlaps several upstreams. A real Fern program imports two async
// functions from distinct interfaces and sums them colorlessly:
//
//	@import("test:a/d","one") async function one(): i32;
//	@import("test:b/d","two") async function two(): i32;
//	async function run(): i32 { return one() + two(); }
//
// Each import lowers with its own `canon lower async` over the consumer's memory
// via its own trampoline+fixup; the two providers (returning 40 and 2) are
// bundled as separate nested components. Running `run()` under wasmtime's async
// features returns 42 — both awaits resolve and compose.
func TestWasmP3AsyncImportMultiFromFern(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	src := `@import("test:a/d", "one") async function one(): i32;
@import("test:b/d", "two") async function two(): i32;
async function run(): i32 { return one() + two(); }
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
	if err := constfold.Fold(prog, nil); err != nil {
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

	i32 := []byte{encode.ValtypeI32}
	comp := component.BuildAsyncImportsAwaitComponent(core, []component.AsyncImportSpec{
		{
			Iface: "test:a/d", WITName: "one",
			Provider:           component.BuildAsyncLiftedExportComponent(p3AsyncCore40, "run", "one", component.CValtypeU32),
			ProviderExportName: "one",
			LowerParams:        i32, LowerResults: i32,
		},
		{
			Iface: "test:b/d", WITName: "two",
			Provider:           component.BuildAsyncLiftedExportComponent(p3AsyncCore2, "run", "two", component.CValtypeU32),
			ProviderExportName: "two",
			LowerParams:        i32, LowerResults: i32,
		},
	}, "__async_run", "run", component.CValtypeU32)

	p := filepath.Join(dir, "fern_async_multi.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async multi-import): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("Fern async multi-import: got %q, want 42", bytes.TrimSpace(out))
	}
}

// TestWasmP3AsyncImportI64ResultFromFern extends the colorless async-import
// vertical to a 64-bit result. A real Fern `@import(...) async function big():
// u64` is awaited colorlessly; the lowered import is the same
// `(retptr) -> i32 status` shape, but the result occupies 8 bytes of the return
// area, so the wrapper reads it with i64.load. The bundled provider delivers
// 4294967338 (2^32 + 42 — a value that does not fit in 32 bits, so a truncated
// read would fail the check). `run` keeps the i32 export shape (the async export
// side is i32-only today) and returns 42 iff the awaited u64 matches — proving
// the wrapper's wide return-area read is correct end to end under wasmtime.
func TestWasmP3AsyncImportI64ResultFromFern(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	src := `@import("test:dep/d", "big") async function big(): u64;
async function run(): i32 {
	var x: u64 = big();
	if (x == 4294967338) { return 42; }
	return 0;
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
	if err := constfold.Fold(prog, nil); err != nil {
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

	// Provider: `big: async func() -> u64` returning 4294967338.
	provider := component.BuildAsyncLiftedExportComponent(p3AsyncBigCoreModule, "big", "big", component.CValtypeU64)

	// The async-lowered import is `(retptr) -> status` = (i32) -> (i32); the u64
	// result is delivered through the 8-byte return area (read as i64).
	// The import `big` returns u64 while the consumer export `run` returns
	// i32, so the import's component functype must be declared `() -> u64`
	// independently of the lift's `() -> u32` — hence the spec form with
	// ImportResultValtype set.
	comp := component.BuildAsyncImportsAwaitComponent(
		core, []component.AsyncImportSpec{{
			Iface: "test:dep/d", WITName: "big",
			Provider: provider, ProviderExportName: "big",
			LowerParams:         []byte{encode.ValtypeI32},
			LowerResults:        []byte{encode.ValtypeI32},
			ImportResultValtype: component.CValtypeU64,
		}},
		"__async_run", "run",
		component.CValtypeU32, // run's own (export) result stays i32
	)

	p := filepath.Join(dir, "fern_async_i64.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async import i64 result): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("Fern async import i64 result: got %q, want 42", bytes.TrimSpace(out))
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
// payoff, assembled entirely through the Go composer (no wac, no
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
	// sibling provider instance into it — the same shape the composer's
	// BuildAsyncImportsAwaitComponent now emits.
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
