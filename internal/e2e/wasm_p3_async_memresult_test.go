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

// p3EchoStringCore is an async-export provider core for `echo(s: string): string`
// — a STRING parameter AND a STRING result: it imports the string-flavored
// ("", "task-return") (ptr, len)->(), exports its memory "mem", a real bump
// "cabi_realloc" (a global cursor from 64), and "echo" (ptr, len)->() which
// simply task-returns its own (ptr, len) — echoing the incoming bytes (already
// materialised in its memory by the lift's realloc) straight back as the result.
// Generated from WAT via wasm-tools 1.240.
var p3EchoStringCore = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x0e, 0x02, 0x60,
	0x02, 0x7f, 0x7f, 0x00, 0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f,
	0x02, 0x10, 0x01, 0x00, 0x0b, 0x74, 0x61, 0x73, 0x6b, 0x2d, 0x72, 0x65,
	0x74, 0x75, 0x72, 0x6e, 0x00, 0x00, 0x03, 0x03, 0x02, 0x01, 0x00, 0x05,
	0x03, 0x01, 0x00, 0x01, 0x06, 0x07, 0x01, 0x7f, 0x01, 0x41, 0xc0, 0x00,
	0x0b, 0x07, 0x1d, 0x03, 0x03, 0x6d, 0x65, 0x6d, 0x02, 0x00, 0x0c, 0x63,
	0x61, 0x62, 0x69, 0x5f, 0x72, 0x65, 0x61, 0x6c, 0x6c, 0x6f, 0x63, 0x00,
	0x01, 0x04, 0x65, 0x63, 0x68, 0x6f, 0x00, 0x02, 0x0a, 0x1c, 0x02, 0x11,
	0x01, 0x01, 0x7f, 0x23, 0x00, 0x21, 0x04, 0x23, 0x00, 0x20, 0x03, 0x6a,
	0x24, 0x00, 0x20, 0x04, 0x0b, 0x08, 0x00, 0x20, 0x00, 0x20, 0x01, 0x10,
	0x00, 0x0b, 0x00, 0x28, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x01, 0x05, 0x01,
	0x00, 0x02, 0x74, 0x72, 0x02, 0x12, 0x02, 0x01, 0x01, 0x04, 0x01, 0x70,
	0x02, 0x02, 0x00, 0x03, 0x70, 0x74, 0x72, 0x01, 0x03, 0x6c, 0x65, 0x6e,
	0x07, 0x06, 0x01, 0x00, 0x03, 0x63, 0x75, 0x72,
}

// TestWasmP3AsyncImportStringParamStringResultFromFern is the
// composite-param-AND-result colorless async-import vertical from REAL Fern
// source — the HTTP-like edge shape that both passes and returns linear-memory
// data:
//
//	@import("test:dep/d","echo") async function echo(s: string): string;
//	async function run(): i32 { var r: string = echo("hello"); return r.len(); }
//
// The wasmbin async-import wrapper (the generalised buildExternAsyncMemParamWrapper)
// marshals the "hello" argument to a canonical (ptr, len) in the consumer's
// memory, runs the `canon lower async + realloc` call `(ptr, len, retptr) ->
// status`, awaits, and lifts the (ptr, len) the host wrote at the return area
// into a Fern string (the host materialised the result bytes in the consumer's
// memory via the lower's realloc option). The bundled provider materialises the
// incoming string in its own memory and echoes it back through the string
// task.return (gMem-trampolined). Running `run()` under wasmtime's async features
// returns 5 (len "hello") — proving a mem param AND a composite result flow
// together across an awaited import, the last param×result quadrant. See
// docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func TestWasmP3AsyncImportStringParamStringResultFromFern(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	src := `@import("test:dep/d", "echo") async function echo(s: string): string;
async function run(): i32 { var r: string = echo("hello"); return r.len(); }
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

	// Provider: `echo: async func(s: string) -> string` returning its input.
	provider := component.BuildAsyncLiftedExportComponentStringParamStringResult(
		p3EchoStringCore, "mem", "cabi_realloc", "echo", "echo")

	i32 := encode.ValtypeI32
	comp := component.BuildAsyncImportsAwaitComponent(core, []component.AsyncImportSpec{{
		Iface: "test:dep/d", WITName: "echo",
		Provider:           provider,
		ProviderExportName: "echo",
		LowerParams:        []byte{i32, i32, i32}, // (ptr, len, retptr)
		LowerResults:       []byte{i32},           // status
		NeedsRealloc:       true,                  // string RESULT materialised in consumer memory
		ImportParamNames:   []string{"s"},         // echo: async func(s: string) -> string
		ImportParamVals:    [][]byte{{component.CValtypeString}},
		ImportResultVal:    []byte{component.CValtypeString},
	}}, "__async_run", "run", component.CValtypeU32)

	p := filepath.Join(dir, "fern_async_strps.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async string param+result import): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("5")) {
		t.Errorf("Fern async string param+result import: got %q, want 5 (len \"hello\")", bytes.TrimSpace(out))
	}
}
