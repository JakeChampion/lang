package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestExternListResultRunsUnderWasmtime is P4c's first gate (composite results
// across the @import boundary — docs/WIT-BRING-YOUR-OWN.md): a `list<u8>` /
// `string` result is lifted from the canonical-ABI return area into a Fern
// string. `wasi:random/random@0.2.0` `get-random-bytes: func(len: u64) ->
// list<u8>` is the simplest runnable composite (scalar param, variable-size
// result, no resources); declared on the Fern side as returning `string`
// (identical canonical ABI), the call must produce a string of exactly the
// requested byte length.
func TestExternListResultRunsUnderWasmtime(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()

	// 16 random bytes → a 16-byte Fern string; the program writes a sentinel
	// only when the lifted length is exactly right (deterministic despite the
	// random contents).
	src := `@import("wasi:random/random@0.2.0", "get-random-bytes")
function rand_bytes(n: u64): string;

function main(): i32 {
	// A heap allocation before the extern call leaves the bump cursor at an
	// odd offset, so the wrapper's return area must be aligned (regression
	// guard for the canonical "pointer not aligned" trap).
	var pad: string = string_from_bytes_unchecked([65 as u8]);
	var s: string = rand_bytes(16 as u64);
	if (pad.len() == 1 && s.len() == 16) { write("len-ok"); } else { write("len-bad"); }
	return 0;
}`
	mainPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}

	// Build the component-run core directly (the CLI's legacy composer doesn't
	// know get-random-bytes; the world-driven path below does).
	info, prog := loadCheckMono(t, mainPath)
	core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
		SynthCliRun:        true,
		PrintMainResult:    true,
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}

	// Codegen gate: the raw import of the declared (interface, wit-name) is
	// present, and cabi_realloc is exported for the host to materialize the
	// returned bytes.
	if !bytes.Contains(core, []byte("wasi:random/random@0.2.0")) || !bytes.Contains(core, []byte("get-random-bytes")) {
		t.Fatalf("core module is missing the extern import")
	}
	if !bytes.Contains(core, []byte("cabi_realloc")) {
		t.Fatalf("core module does not export cabi_realloc")
	}

	w, err := componenttype.DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	comp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto: %v", err)
	}
	mine := filepath.Join(dir, "extern-list.wasm")
	if err := os.WriteFile(mine, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if out, err := exec.Command(wasmtools, "validate", mine).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	out, err := exec.Command(wasmtime, "run", mine).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("len-ok")) {
		t.Fatalf("stdout = %q, want it to contain %q (lifted string had the wrong length)", out, "len-ok")
	}
}

// TestExternU8ArrayResultRunsUnderWasmtime is the u8[] counterpart of the
// string-result gate (P4c): the same `get-random-bytes(len) -> list<u8>`
// import, but declared as returning `u8[]`, lifts zero-copy into a Fern slice.
// The result must have exactly the requested element count.
func TestExternU8ArrayResultRunsUnderWasmtime(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()

	src := `@import("wasi:random/random@0.2.0", "get-random-bytes")
function rand_bytes(n: u64): u8[];

function main(): i32 {
	// Heap-misaligning pre-alloc (return-area alignment guard).
	var pad: string = string_from_bytes_unchecked([65 as u8]);
	var a: u8[] = rand_bytes(16 as u64);
	if (pad.len() == 1 && a.len() == 16) { write("arr-ok"); } else { write("arr-bad"); }
	return 0;
}`
	mainPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}

	info, prog := loadCheckMono(t, mainPath)
	core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
		SynthCliRun:        true,
		PrintMainResult:    true,
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	if !bytes.Contains(core, []byte("get-random-bytes")) {
		t.Fatalf("core module is missing the extern import")
	}
	if !bytes.Contains(core, []byte("cabi_realloc")) {
		t.Fatalf("core module does not export cabi_realloc")
	}

	w, err := componenttype.DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	comp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto: %v", err)
	}
	mine := filepath.Join(dir, "extern-u8arr.wasm")
	if err := os.WriteFile(mine, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	if out, err := exec.Command(wasmtools, "validate", mine).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate: %v\n%s", err, out)
	}
	out, err := exec.Command(wasmtime, "run", mine).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("arr-ok")) {
		t.Fatalf("stdout = %q, want it to contain %q", out, "arr-ok")
	}
}
