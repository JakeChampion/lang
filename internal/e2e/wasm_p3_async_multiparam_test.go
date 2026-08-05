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

// p3FetchScalarCore is an async-export provider core for a MIXED multi-parameter
// import `fetch(url: string, n: i32)`: it imports ("", "task-return") (i32)->(),
// exports its memory "mem", a real bump "cabi_realloc" (a global cursor from 16),
// and "fetch" (ptr, len, n)->() which task-returns `len + n` (the canonical
// flattening of a `(string, u32)` param pair + a scalar). Generated from WAT via
// wasm-tools 1.240.
var p3FetchScalarCore = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x13, 0x03, 0x60,
	0x01, 0x7f, 0x00, 0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f, 0x60,
	0x03, 0x7f, 0x7f, 0x7f, 0x00, 0x02, 0x10, 0x01, 0x00, 0x0b, 0x74, 0x61,
	0x73, 0x6b, 0x2d, 0x72, 0x65, 0x74, 0x75, 0x72, 0x6e, 0x00, 0x00, 0x03,
	0x03, 0x02, 0x01, 0x02, 0x05, 0x03, 0x01, 0x00, 0x01, 0x06, 0x06, 0x01,
	0x7f, 0x01, 0x41, 0x10, 0x0b, 0x07, 0x1e, 0x03, 0x03, 0x6d, 0x65, 0x6d,
	0x02, 0x00, 0x0c, 0x63, 0x61, 0x62, 0x69, 0x5f, 0x72, 0x65, 0x61, 0x6c,
	0x6c, 0x6f, 0x63, 0x00, 0x01, 0x05, 0x66, 0x65, 0x74, 0x63, 0x68, 0x00,
	0x02, 0x0a, 0x1d, 0x02, 0x11, 0x01, 0x01, 0x7f, 0x23, 0x00, 0x21, 0x04,
	0x23, 0x00, 0x20, 0x03, 0x6a, 0x24, 0x00, 0x20, 0x04, 0x0b, 0x09, 0x00,
	0x20, 0x01, 0x20, 0x02, 0x6a, 0x10, 0x00, 0x0b, 0x00, 0x2b, 0x04, 0x6e,
	0x61, 0x6d, 0x65, 0x01, 0x05, 0x01, 0x00, 0x02, 0x74, 0x72, 0x02, 0x15,
	0x02, 0x01, 0x01, 0x04, 0x01, 0x70, 0x02, 0x03, 0x00, 0x03, 0x70, 0x74,
	0x72, 0x01, 0x03, 0x6c, 0x65, 0x6e, 0x02, 0x01, 0x6e, 0x07, 0x06, 0x01,
	0x00, 0x03, 0x63, 0x75, 0x72,
}

// TestWasmP3AsyncImportMixedMultiParamFromFern is the multi-arg edge-handler
// colorless async-import vertical from REAL Fern source: an async import takes
// BOTH a string and a scalar argument —
//
//	@import("test:dep/d","fetch") async function fetch(url: string, n: i32): i32;
//	async function run(): i32 { return fetch("hi", 40); }
//
// The wasmbin async-import wrapper marshals each argument to its canonical slot(s)
// in declaration order — the string "hi" SSO-normalised to (ptr, len) in the
// consumer's memory, the scalar 40 forwarded as-is — appends the return-area
// pointer, and runs the `canon lower async` call `(ptr, len, n, retptr) ->
// status` (memory option only — the param bytes are the caller's). The bundled
// provider receives `(string, u32)`, materialises the string in its own memory
// via its bump cabi_realloc, and task-returns `len + n`. Running `run()` under
// wasmtime's async features returns 42 (len "hi" = 2, + 40) — proving a mixed
// multi-argument call flows colorlessly into an awaited import, the realistic
// edge-handler shape. See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func TestWasmP3AsyncImportMixedMultiParamFromFern(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	src := `@import("test:dep/d", "fetch") async function fetch(url: string, n: i32): i32;
async function run(): i32 { return fetch("hi", 40); }
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

	// Provider: `fetch: async func(url: string, n: u32) -> u32` returning len + n.
	provider := component.BuildAsyncLiftedExportComponentMemParams(
		p3FetchScalarCore, "mem", "cabi_realloc", "fetch", "fetch",
		[]string{"url", "n"},
		[][]byte{{component.CValtypeString}, {component.CValtypeU32}},
		component.CValtypeU32,
	)

	i32 := encode.ValtypeI32
	comp := component.BuildAsyncImportsAwaitComponent(core, []component.AsyncImportSpec{{
		Iface: "test:dep/d", WITName: "fetch",
		Provider:           provider,
		ProviderExportName: "fetch",
		LowerParams:        []byte{i32, i32, i32, i32}, // (ptr, len, n, retptr)
		LowerResults:       []byte{i32},                // status
		NeedsRealloc:       false,                      // string/scalar PARAMs: caller owns the bytes
		// fetch: async func(url: string, n: u32) -> u32
		ImportParamNames: []string{"url", "n"},
		ImportParamVals:  [][]byte{{component.CValtypeString}, {component.CValtypeU32}},
	}}, "__async_run", "run", component.CValtypeU32)

	p := filepath.Join(dir, "fern_async_multiparam.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async mixed multi-param import): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("Fern async mixed multi-param import: got %q, want 42 (len \"hi\" + 40)", bytes.TrimSpace(out))
	}
}
