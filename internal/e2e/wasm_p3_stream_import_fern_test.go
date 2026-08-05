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

// TestWasmP3StreamImportFromFern is the colorless `stream[T]` payoff from REAL
// Fern source — the capstone of the stream surface (docs/STREAM-TYPE-SURFACE.md):
//
//	@import("test:dep/d","prod") async function body(): stream[u8];
//	async function run(): i32 {
//	    var b: u8[] = body();                 // colorless: collects the stream to u8[]
//	    return (b[0] as i32) + (b[1] as i32) + (b[2] as i32);
//	}
//
// The checker rewrites the `stream[u8]` result to `u8[]` (colorless collect) and
// stashes the element type; the wasmbin collect-wrapper drives `stream.read` + the
// await loop into a grow-on-demand `u8[]` until EOF. The WASMBIN-GENERATED
// consumer (which exports its own memory) is composed against the bundled EOF
// producer via BuildAsyncStreamImportComponent — the dep-lower, waitable-set.wait
// and stream.read each trampolined over the consumer's exported memory. Running
// `run()` under wasmtime's async features returns 42 (10+20+12) — a `stream[u8]`
// flows colorlessly from a host export into Fern source as a collected array,
// driving the slice-3a collect-wrapper end to end.
func TestWasmP3StreamImportFromFern(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	src := `@import("test:dep/d", "prod") async function body(): stream[u8];
async function run(): i32 {
	var b: u8[] = body();
	return (b[0] as i32) + (b[1] as i32) + (b[2] as i32);
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

	// Bundle the EOF producer (writes [10,20,12], write-awaits, drops writable).
	comp := component.BuildAsyncStreamImportComponent(
		core, p3StreamEOFProducerCore, p3Stream2SlotTramp, p3Stream2SlotFixup,
		"test:dep/d", "prod", "__async_run", "run",
		component.CValtypeU8, component.CValtypeU32,
	)

	p := filepath.Join(dir, "fern_stream_import.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (stream import from Fern): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("Fern stream[u8] import: got %q, want 42 (10+20+12)", bytes.TrimSpace(out))
	}
}
