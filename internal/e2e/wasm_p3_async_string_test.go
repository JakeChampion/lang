package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
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
