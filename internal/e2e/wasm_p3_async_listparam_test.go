package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
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
