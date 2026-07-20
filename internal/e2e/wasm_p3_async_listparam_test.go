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

// TestWasmP3AsyncListParamExportProvider runtime-verifies the numeric-`list<T>`
// PARAM side of the async canonical ABI: an async export that takes a `list<u8>`
// argument. component.BuildAsyncLiftedExportComponentListParam lifts the core
// `recv` (reused from the string-param provider p3SendStringCore — a `list<u8>`
// parameter flattens to the same `(ptr, len)` core args as a string, and the
// core just task-returns the length) as `recv: async func(xs: list<u8>) -> u32`
// with a `[async, memory, realloc]` lift over a defined `list<u8>` param type.
// Running `recv([104,101,108,108,111])` (the "hello" bytes) under wasmtime's
// async features returns 5 (its length) — proving a numeric list flows in
// correctly as a defined-type parameter. See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func TestWasmP3AsyncListParamExportProvider(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	comp := component.BuildAsyncLiftedExportComponentListParam(p3SendStringCore, "mem", "cabi_realloc", "send", "recv", component.CValtypeU8, component.CValtypeU32)
	dir := t.TempDir()
	p := filepath.Join(dir, "recvparam.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "recv([104,101,108,108,111])", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async list param): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("5")) {
		t.Errorf("async list param: got %q, want 5 (len of 5-byte list)", bytes.TrimSpace(out))
	}
}

// TestWasmP3AsyncImportListParamFromFern is the numeric-`list<T>` PARAM colorless
// async-import vertical from REAL Fern source: a program passes a `u8[]` argument
// to an async import —
//
//	@import("test:dep/d","recv") async function recv(xs: u8[]): i32;
//	async function run(): i32 {
//	    var xs: u8[] = [104 as u8, 101 as u8, 108 as u8, 108 as u8, 111 as u8];
//	    return recv(xs);
//	}
//
// The wasmbin async-import branch forwards the array's canonical (ptr, len) =
// (elemPtr, load(elemPtr-4)) — no normalisation, the elements are already packed
// at native stride — and runs the `canon lower async` call `(ptr, len, retptr) ->
// status` (memory option only — the param bytes are the caller's, no realloc on
// the consumer side); the bundled provider receives the list, materialises it in
// its own memory via its bump cabi_realloc, and task-returns its length. Running
// `run()` under wasmtime's async features returns 5 — a numeric array flows
// colorlessly into an awaited import. The array counterpart of
// TestWasmP3AsyncImportStringParamFromFern. See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func TestWasmP3AsyncImportListParamFromFern(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	src := `@import("test:dep/d", "recv") async function recv(xs: u8[]): i32;
async function run(): i32 {
	var xs: u8[] = [104 as u8, 101 as u8, 108 as u8, 108 as u8, 111 as u8];
	return recv(xs);
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

	// Provider: `recv: async func(xs: list<u8>) -> u32` returning the list length.
	provider := component.BuildAsyncLiftedExportComponentListParam(p3SendStringCore, "mem", "cabi_realloc", "send", "recv", component.CValtypeU8, component.CValtypeU32)

	i32 := encode.ValtypeI32
	comp := component.BuildAsyncImportsAwaitComponent(core, []component.AsyncImportSpec{{
		Iface: "test:dep/d", WITName: "recv",
		Provider:           provider,
		ProviderExportName: "recv",
		LowerParams:        []byte{i32, i32, i32}, // (ptr, len, retptr)
		LowerResults:       []byte{i32},           // status
		NeedsRealloc:       false,                 // list PARAM: caller owns the bytes, no realloc
		// recv: async func(xs: list<u8>) -> u32. list<u8> is defined type 0;
		// the param references it by index (sleb 0).
		ImportDefinedTypes: [][]byte{component.InnerTypeList(component.CValtypeU8)},
		ImportParamNames:   []string{"xs"},
		ImportParamVals:    [][]byte{{0x00}},
	}}, "__async_run", "run", component.CValtypeU32)

	p := filepath.Join(dir, "fern_async_listparam.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async list param import): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("5")) {
		t.Errorf("Fern async list param import: got %q, want 5 (len of 5-byte u8[])", bytes.TrimSpace(out))
	}
}
