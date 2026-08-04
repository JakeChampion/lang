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
)

// TestWasmP3StreamForIn locks in that `for x in <stream-returning call>` iterates
// a u8 stream RESULT directly — no intermediate `var b: u8[] = …` needed:
//
//	@import("test:dep/d","prod") async function body(): stream[u8];
//	async function run(): i32 {
//	    var sum: i32 = 0;
//	    for x in body() { sum = sum + (x as i32); }   // LAZY: one element at a time
//	    return sum;
//	}
//
// This runs LAZILY (L2, docs/STREAM-TYPE-SURFACE.md): the parser leaves the
// for-in as an ast.ForEach, and the checker lowers it to a per-element read loop
// — `var h = body$open(); while (true) { var v = __stream_next_u8(h); if v < 0
// break; var x = v as u8; BODY } __stream_drop(h)` — so each byte is pulled off
// the wire (read + await) as the loop turns, never materialising the whole
// sequence. Result is identical to the eager collect-then-iterate form; the
// structural difference is pinned by the checker test
// TestStreamForEachDesugarsToLazyLoop. Runs against the EOF producer → 42.
//
// (A non-u8 stream, or a value-context `var b: u8[] = body()`, still collects
// eagerly via the collect-wrapper — the `-1` EOF sentinel of the per-element read
// is unambiguous only for byte elements. See docs/STREAM-TYPE-SURFACE.md.)
func TestWasmP3StreamForIn(t *testing.T) {
	skipIfPreview2Missing(t)

	src := `@import("test:dep/d", "prod") async function body(): stream[u8];
async function run(): i32 {
	var sum: i32 = 0;
	for x in body() {
		sum = sum + (x as i32);
	}
	return sum;
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

	comp := component.BuildAsyncStreamImportComponent(
		core, p3StreamEOFProducerCore, p3Stream2SlotTramp, p3Stream2SlotFixup,
		"test:dep/d", "prod", "__async_run", "run",
		component.CValtypeU8, component.CValtypeU32,
	)

	p := filepath.Join(dir, "fern_stream_forin.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (for-in over stream): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("for-in over stream[u8]: got %q, want 42 (10+20+12)", bytes.TrimSpace(out))
	}
}
