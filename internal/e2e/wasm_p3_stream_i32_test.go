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

// p3StreamI32EOFProducerCore is the i32 (stride-4) variant of the EOF producer:
// `prod` creates a stream<s32>, task.returns the readable end, writes [10,20,12]
// as i32 (4-byte elements at mem[0],[4],[8]), AWAITS the write, then
// stream.drop-writable (EOF). Imports/exports identical to p3StreamEOFProducerCore.
// Generated from WAT via wasm-tools 1.240.
var p3StreamI32EOFProducerCore = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x22, 0x07, 0x60,
	0x01, 0x7f, 0x00, 0x60, 0x00, 0x01, 0x7e, 0x60, 0x03, 0x7f, 0x7f, 0x7f,
	0x01, 0x7f, 0x60, 0x00, 0x01, 0x7f, 0x60, 0x02, 0x7f, 0x7f, 0x00, 0x60,
	0x02, 0x7f, 0x7f, 0x01, 0x7f, 0x60, 0x00, 0x00, 0x02, 0x37, 0x08, 0x00,
	0x02, 0x74, 0x72, 0x00, 0x00, 0x00, 0x04, 0x73, 0x6e, 0x65, 0x77, 0x00,
	0x01, 0x00, 0x02, 0x73, 0x77, 0x00, 0x02, 0x00, 0x03, 0x73, 0x64, 0x77,
	0x00, 0x00, 0x00, 0x03, 0x77, 0x73, 0x6e, 0x00, 0x03, 0x00, 0x02, 0x77,
	0x6a, 0x00, 0x04, 0x00, 0x03, 0x77, 0x73, 0x77, 0x00, 0x05, 0x00, 0x03,
	0x77, 0x73, 0x64, 0x00, 0x00, 0x03, 0x02, 0x01, 0x06, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x07, 0x0e, 0x02, 0x03, 0x6d, 0x65, 0x6d, 0x02, 0x00, 0x04,
	0x70, 0x72, 0x6f, 0x64, 0x00, 0x08, 0x0a, 0x64, 0x01, 0x62, 0x02, 0x01,
	0x7e, 0x04, 0x7f, 0x10, 0x01, 0x21, 0x00, 0x20, 0x00, 0xa7, 0x21, 0x01,
	0x20, 0x00, 0x42, 0x20, 0x88, 0xa7, 0x21, 0x02, 0x41, 0x00, 0x41, 0x0a,
	0x36, 0x02, 0x00, 0x41, 0x04, 0x41, 0x14, 0x36, 0x02, 0x00, 0x41, 0x08,
	0x41, 0x0c, 0x36, 0x02, 0x00, 0x20, 0x01, 0x10, 0x00, 0x20, 0x02, 0x41,
	0x00, 0x41, 0x03, 0x10, 0x02, 0x21, 0x03, 0x20, 0x03, 0x41, 0x7f, 0x46,
	0x04, 0x40, 0x10, 0x04, 0x21, 0x04, 0x20, 0x02, 0x20, 0x04, 0x10, 0x05,
	0x20, 0x04, 0x41, 0xc0, 0x00, 0x10, 0x06, 0x1a, 0x20, 0x02, 0x41, 0x00,
	0x10, 0x05, 0x20, 0x04, 0x10, 0x07, 0x0b, 0x20, 0x02, 0x10, 0x03, 0x0b,
	0x00, 0x46, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x01, 0x27, 0x08, 0x00, 0x02,
	0x74, 0x72, 0x01, 0x04, 0x73, 0x6e, 0x65, 0x77, 0x02, 0x02, 0x73, 0x77,
	0x03, 0x03, 0x73, 0x64, 0x77, 0x04, 0x03, 0x77, 0x73, 0x6e, 0x05, 0x02,
	0x77, 0x6a, 0x06, 0x03, 0x77, 0x73, 0x77, 0x07, 0x03, 0x77, 0x73, 0x64,
	0x02, 0x16, 0x01, 0x08, 0x05, 0x00, 0x01, 0x68, 0x01, 0x02, 0x72, 0x64,
	0x02, 0x02, 0x77, 0x72, 0x03, 0x02, 0x72, 0x73, 0x04, 0x02, 0x77, 0x73,
}

// TestWasmP3StreamI32ResultFromFern exercises the colorless `stream[T]` result
// collect-wrapper with a NON-trivial element stride (i32 = 4 bytes), the coverage
// the u8 e2es can't give: the wrapper's `total*stride` / capacity-doubling /
// per-read `count` math is stride-scaled. Real Fern:
//
//	@import("test:dep/d","prod") async function body(): stream[i32];
//	async function run(): i32 { var b: i32[] = body(); return b[0] + b[1] + b[2]; }
//
// composed against the i32 EOF producer (writes [10,20,12] as i32, write-awaits,
// drops) → 42. Confirms the stride-aware collect path on stream<s32>.
func TestWasmP3StreamI32ResultFromFern(t *testing.T) {
	skipIfPreview2Missing(t)

	src := `@import("test:dep/d", "prod") async function body(): stream[i32];
async function run(): i32 {
	var b: i32[] = body();
	return b[0] + b[1] + b[2];
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
		core, p3StreamI32EOFProducerCore, p3Stream2SlotTramp, p3Stream2SlotFixup,
		"test:dep/d", "prod", "__async_run", "run",
		component.CValtypeS32, component.CValtypeU32,
	)

	p := filepath.Join(dir, "fern_stream_i32.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (stream[i32] result from Fern): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("Fern stream[i32] result: got %q, want 42 (10+20+12)", bytes.TrimSpace(out))
	}
}

// TestWasmP3StreamI32ForIn exercises LAZY `for x in body()` over a NON-u8 stream
// (i32) — the general-T coverage the u8 lazy e2e can't give. Real Fern:
//
//	@import("test:dep/d","prod") async function body(): stream[i32];
//	async function run(): i32 { var sum: i32 = 0; for x in body() { sum = sum + x; } return sum; }
//
// The checker desugars it to the cursor loop (`var c = body$open(); while(true){
// if (__stream_next(c) == 0) break; var x = __stream_elem_i32(c); … } __stream_drop(c)`)
// — separating the EOF flag from the value read so a real `-1` element would
// never be mistaken for EOF (the u8-only `-1` sentinel limitation L2 had). Each
// i32 is pulled off the wire one at a time. Composed against the i32 EOF producer
// (writes [10,20,12], write-awaits, drops) → 42. See docs/STREAM-TYPE-SURFACE.md.
func TestWasmP3StreamI32ForIn(t *testing.T) {
	skipIfPreview2Missing(t)

	src := `@import("test:dep/d", "prod") async function body(): stream[i32];
async function run(): i32 {
	var sum: i32 = 0;
	for x in body() {
		sum = sum + x;
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
		core, p3StreamI32EOFProducerCore, p3Stream2SlotTramp, p3Stream2SlotFixup,
		"test:dep/d", "prod", "__async_run", "run",
		component.CValtypeS32, component.CValtypeU32,
	)

	p := filepath.Join(dir, "fern_stream_i32_forin.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (lazy for-in over stream[i32]): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("lazy for-in over stream[i32]: got %q, want 42 (10+20+12)", bytes.TrimSpace(out))
	}
}
