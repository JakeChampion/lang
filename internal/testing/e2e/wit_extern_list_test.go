package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

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

	src := `@import("wasi:random/random@0.2.0", "get-random-bytes")
function rand_bytes(n: u64): string;

function main(): i32 {
	// A heap allocation before the extern call leaves the bump cursor at an
	// odd offset, so the wrapper's return area must be aligned (regression
	// guard for the canonical "pointer not aligned" trap).
	let pad: string = string_from_bytes_unchecked([65 as u8]);
	let s: string = rand_bytes(16 as u64);
	if (pad.len() == 1 && s.len() == 16) { write("len-ok"); } else { write("len-bad"); }
	return 0;
}`
	mainPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}

	core := e2eharness.SelfHostComponentCore(t, mainPath)

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
	let pad: string = string_from_bytes_unchecked([65 as u8]);
	let a: u8[] = rand_bytes(16 as u64);
	if (pad.len() == 1 && a.len() == 16) { write("arr-ok"); } else { write("arr-bad"); }
	return 0;
}`
	mainPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}

	core := e2eharness.SelfHostComponentCore(t, mainPath)
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
