package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
)

// p3SendStringCore is an async-export provider core for a STRING PARAMETER: it
// imports ("", "task-return") (i32)->(), exports its memory "mem", a real bump
// "cabi_realloc" (a global cursor starting at 16 — a constant-returning realloc
// fails because the async ABI calls it more than once), and "send" (ptr,len)->()
// which task-returns the param's length. Generated from WAT via wasm-tools 1.240
// and verified to run under wasmtime (`send("hello") -> 5`).
var p3SendStringCore = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x12, 0x03, 0x60,
	0x01, 0x7f, 0x00, 0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f, 0x60,
	0x02, 0x7f, 0x7f, 0x00, 0x02, 0x10, 0x01, 0x00, 0x0b, 0x74, 0x61, 0x73,
	0x6b, 0x2d, 0x72, 0x65, 0x74, 0x75, 0x72, 0x6e, 0x00, 0x00, 0x03, 0x03,
	0x02, 0x01, 0x02, 0x05, 0x03, 0x01, 0x00, 0x01, 0x06, 0x06, 0x01, 0x7f,
	0x01, 0x41, 0x10, 0x0b, 0x07, 0x1d, 0x03, 0x03, 0x6d, 0x65, 0x6d, 0x02,
	0x00, 0x0c, 0x63, 0x61, 0x62, 0x69, 0x5f, 0x72, 0x65, 0x61, 0x6c, 0x6c,
	0x6f, 0x63, 0x00, 0x01, 0x04, 0x73, 0x65, 0x6e, 0x64, 0x00, 0x02, 0x0a,
	0x1a, 0x02, 0x11, 0x01, 0x01, 0x7f, 0x23, 0x00, 0x21, 0x04, 0x23, 0x00,
	0x20, 0x03, 0x6a, 0x24, 0x00, 0x20, 0x04, 0x0b, 0x06, 0x00, 0x20, 0x01,
	0x10, 0x00, 0x0b, 0x00, 0x28, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x01, 0x05,
	0x01, 0x00, 0x02, 0x74, 0x72, 0x02, 0x12, 0x02, 0x01, 0x01, 0x04, 0x01,
	0x70, 0x02, 0x02, 0x00, 0x03, 0x70, 0x74, 0x72, 0x01, 0x03, 0x6c, 0x65,
	0x6e, 0x07, 0x06, 0x01, 0x00, 0x03, 0x63, 0x75, 0x72,
}

// TestWasmP3AsyncStringParamExportProvider runtime-verifies the composite-PARAM
// side of the async canonical ABI: an async export that takes a `string`
// argument. component.BuildAsyncLiftedExportComponentStringParam lifts the core
// `send` as `send: async func(s: string) -> u32` with a `[async, memory,
// realloc]` lift — the canonical ABI materialises the incoming string in the
// export's memory via its bump cabi_realloc before the core runs (the core sig
// is the plain `(ptr, len) -> ()`, result via scalar task.return; there is no
// memory-circularity because the result is scalar). Running `send("hello")`
// under wasmtime's async features returns 5 (its length) — proving the param
// flows in correctly and correcting the earlier "blocked" note (the blocker was
// a constant-returning cabi_realloc, not the async-lift param ABI). See
// docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func TestWasmP3AsyncStringParamExportProvider(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	comp := component.BuildAsyncLiftedExportComponentStringParam(p3SendStringCore, "mem", "cabi_realloc", "send", "send", component.CValtypeU32)
	dir := t.TempDir()
	p := filepath.Join(dir, "sendparam.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", `send("hello")`, p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async string param): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("5")) {
		t.Errorf("async string param: got %q, want 5 (len \"hello\")", bytes.TrimSpace(out))
	}
}
