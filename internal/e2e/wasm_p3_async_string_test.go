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

// p3FetchStringCore is an async-export provider core that returns a STRING:
// it imports ("", "task-return") (i32,i32)->() — the string task.return takes
// (ptr, len) — exports its linear memory as "mem", and its "fetch" export
// (()->()) task-returns the 5-byte "hello" held in a data segment at offset 8.
var p3FetchStringCore = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x09, 0x02, 0x60, 0x02, 0x7f, 0x7f, 0x00, 0x60, 0x00, 0x00, // types: (i32,i32)->(), ()->()
	0x02, 0x10, 0x01, 0x00, 0x0b, 't', 'a', 's', 'k', '-', 'r', 'e', 't', 'u', 'r', 'n', 0x00, 0x00,
	0x03, 0x02, 0x01, 0x01, // func section: 1 func of type 1
	0x05, 0x03, 0x01, 0x00, 0x01, // memory: min 1
	0x07, 0x0f, 0x02, 0x03, 'm', 'e', 'm', 0x02, 0x00, 0x05, 'f', 'e', 't', 'c', 'h', 0x00, 0x01, // export "mem" mem0, "fetch" func1
	0x0a, 0x0a, 0x01, 0x08, 0x00, 0x41, 0x08, 0x41, 0x05, 0x10, 0x00, 0x0b, // code: i32.const 8, i32.const 5, call 0 (task-return), end
	0x0b, 0x0b, 0x01, 0x00, 0x41, 0x08, 0x0b, 0x05, 'h', 'e', 'l', 'l', 'o', // data: "hello" @ offset 8
}

// TestWasmP3AsyncStringExportProvider runtime-verifies the STRING side of the
// WASI Preview-3 async canonical ABI — the provider half of the
// composite-result async-import vertical (docs/WASI-PREVIEW3-ASYNC-PLAN.md).
// component.BuildAsyncLiftedExportComponentString lifts a core whose `fetch`
// export delivers a string through `task.return` into `fetch: async func() ->
// string`. The string `task.return` carries a `memory` option referencing the
// provider's own memory, but the provider imports task.return — the same
// memory→instance→import circularity the async lower hits — so it's broken with
// the gMem trampoline (placeholder task.return, alias memory, real
// `task.return (string) (memory)`, fixup the table). Running `fetch()` under
// wasmtime's async features prints "hello", which upgrades the
// PutCanonTaskReturnStringWithMemory + PutCanonSectionLiftAsyncWithMemory
// encoders from byte-pinned-by-analogy to runtime-verified.
func TestWasmP3AsyncStringExportProvider(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	comp := component.BuildAsyncLiftedExportComponentString(p3FetchStringCore, "mem", "fetch", "fetch")
	dir := t.TempDir()
	p := filepath.Join(dir, "strprov.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", `fetch()`, p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async string export): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("hello")) {
		t.Errorf("async string export: got %q, want hello", bytes.TrimSpace(out))
	}
}

// TestWasmP3AsyncImportStringFromFern is the full composite-result colorless
// async-import vertical from REAL Fern source: a program awaits a
// string-returning async import and uses the result —
//
//	@import("test:dep/d","fetch") async function fetch(): string;
//	async function run(): i32 { var s: string = fetch(); return s.len(); }
//
// The wasmbin async-import branch lowers `fetch` to the `(retptr) -> status`
// shape and lifts the return-area (ptr,len) into a Fern string;
// BuildAsyncImportsAwaitComponent lowers it with `canon lower async + realloc`
// (NeedsRealloc), aliasing the consumer's cabi_realloc so the host materialises
// the bytes in the consumer's memory, and bundles the proven string provider
// (p3FetchStringCore → "hello"). Running `run()` under wasmtime's async
// features returns 5 (len "hello") — the string flows colorlessly across the
// async lower/lift round-trip. See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func TestWasmP3AsyncImportStringFromFern(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	src := `@import("test:dep/d", "fetch") async function fetch(): string;
async function run(): i32 { var s: string = fetch(); return s.len(); }
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

	// Provider: `fetch: async func() -> string` returning "hello" (len 5).
	provider := component.BuildAsyncLiftedExportComponentString(p3FetchStringCore, "mem", "fetch", "fetch")

	i32 := []byte{encode.ValtypeI32}
	comp := component.BuildAsyncImportsAwaitComponent(core, []component.AsyncImportSpec{{
		Iface: "test:dep/d", WITName: "fetch",
		Provider:            provider,
		ProviderExportName:  "fetch",
		LowerParams:         i32, // (retptr) -> status
		LowerResults:        i32,
		NeedsRealloc:        true,                     // string result: lower carries [async, memory, realloc]
		ImportResultValtype: component.CValtypeString, // fetch: async func() -> string
	}}, "__async_run", "run", component.CValtypeU32)

	p := filepath.Join(dir, "fern_async_string.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async string import): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("5")) {
		t.Errorf("Fern async string import: got %q, want 5 (len \"hello\")", bytes.TrimSpace(out))
	}
}

// TestWasmP3AsyncImportMixedScalarStringFromFern hardens the composer's mixed
// realloc/non-realloc handling: one program awaits a scalar async import AND a
// string async import together, the heterogeneous edge-handler shape —
//
//	@import("test:a/d","num") async function num(): i32;
//	@import("test:b/d","msg") async function msg(): string;
//	async function run(): i32 { return num() + msg().len(); }
//
// `num` lowers with plain `canon lower async`; `msg` lowers with
// `canon lower async + realloc` (NeedsRealloc) — so the single shared
// cabi_realloc alias shifts the real-lower / run core-func indices by one for
// BOTH imports (the reallocOff path), which this exercises. The scalar provider
// returns 40 and the string provider "hello" (len 5); running `run()` under
// wasmtime's async features returns 45.
func TestWasmP3AsyncImportMixedScalarStringFromFern(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	src := `@import("test:a/d", "num") async function num(): i32;
@import("test:b/d", "msg") async function msg(): string;
async function run(): i32 { return num() + msg().len(); }
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
			Iface: "test:a/d", WITName: "num",
			Provider:           component.BuildAsyncLiftedExportComponent(p3AsyncCore40, "run", "num", component.CValtypeU32),
			ProviderExportName: "num",
			LowerParams:        i32, LowerResults: i32,
			NeedsRealloc: false, // scalar result
		},
		{
			Iface: "test:b/d", WITName: "msg",
			Provider:           component.BuildAsyncLiftedExportComponentString(p3FetchStringCore, "mem", "fetch", "msg"),
			ProviderExportName: "msg",
			LowerParams:        i32, LowerResults: i32,
			NeedsRealloc:        true,                     // string result
			ImportResultValtype: component.CValtypeString, // msg: async func() -> string
		},
	}, "__async_run", "run", component.CValtypeU32)

	p := filepath.Join(dir, "fern_async_mixed.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async mixed scalar+string import): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("45")) {
		t.Errorf("Fern async mixed import: got %q, want 45 (40 + len \"hello\")", bytes.TrimSpace(out))
	}
}

// TestWasmP3AsyncListExportProvider runtime-verifies the `list<elem>` side of
// the async canonical ABI — the sibling of the string provider. A `list<u8>`
// is `(ptr, len)` at the canonical ABI exactly like a string, so
// component.BuildAsyncLiftedExportComponentList reuses the same core shape +
// task.return memory trampoline; the only difference is the result is a defined
// `list<u8>` component type (referenced by index) rather than the inline string
// primitive. Reusing p3FetchStringCore (the bytes "hello"), `fetch: async
// func() -> list<u8>` returns [104,101,108,108,111] under wasmtime's async
// features — verifying PutCanonTaskReturnTypeIdxWithMemory + the list lift.
func TestWasmP3AsyncListExportProvider(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	comp := component.BuildAsyncLiftedExportComponentList(p3FetchStringCore, "mem", "fetch", "fetch", component.CValtypeU8)
	dir := t.TempDir()
	p := filepath.Join(dir, "listprov.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", `fetch()`, p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async list export): %v\n%s", err, out)
	}
	// "hello" = [104, 101, 108, 108, 111]; check the first and last bytes appear.
	if !bytes.Contains(out, []byte("104")) || !bytes.Contains(out, []byte("111")) {
		t.Errorf("async list export: got %q, want bytes of \"hello\" (104..111)", bytes.TrimSpace(out))
	}
}

// TestWasmP3AsyncImportListFromFern is the full list<T> composite-result async
// import from REAL Fern source: a program awaits a u8[]-returning async import
// and inspects the lifted array —
//
//	@import("test:dep/d","fetch") async function fetch(): u8[];
//	async function run(): i32 {
//	    var xs: u8[] = fetch();
//	    if (xs.len() == 5 && xs[0] == 104 && xs[4] == 111) { return 42; }
//	    return 0;
//	}
//
// The wasmbin async-import branch lowers `fetch` to `(retptr) -> status` and
// lifts the return-area (ptr,len) into a Fern u8[]; the composer lowers it with
// `canon lower async + realloc` (NeedsRealloc) so the host materialises the
// bytes in the consumer's memory; the bundled provider (p3FetchStringCore typed
// as list<u8>) returns "hello"'s bytes [104,101,108,108,111]. Running `run()`
// under wasmtime's async features returns 42 — the array flows colorlessly with
// the right length AND element values.
func TestWasmP3AsyncImportListFromFern(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	src := `@import("test:dep/d", "fetch") async function fetch(): u8[];
async function run(): i32 {
	var xs: u8[] = fetch();
	if (xs.len() == 5 && xs[0] == 104 && xs[4] == 111) { return 42; }
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

	// Provider: `fetch: async func() -> list<u8>` returning "hello"'s bytes.
	provider := component.BuildAsyncLiftedExportComponentList(p3FetchStringCore, "mem", "fetch", "fetch", component.CValtypeU8)

	i32 := []byte{encode.ValtypeI32}
	comp := component.BuildAsyncImportsAwaitComponent(core, []component.AsyncImportSpec{{
		Iface: "test:dep/d", WITName: "fetch",
		Provider:           provider,
		ProviderExportName: "fetch",
		LowerParams:        i32,
		LowerResults:       i32,
		NeedsRealloc:       true, // list<u8> result: lower carries [async, memory, realloc]
		// fetch: async func() -> list<u8>. The list<u8> is a defined type
		// (component type 0); the result references it by index (sleb 0).
		ImportDefinedTypes: [][]byte{component.InnerTypeList(component.CValtypeU8)},
		ImportResultVal:    []byte{0x00},
	}}, "__async_run", "run", component.CValtypeU32)

	p := filepath.Join(dir, "fern_async_list.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async list import): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("Fern async list import: got %q, want 42", bytes.TrimSpace(out))
	}
}
