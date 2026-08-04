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

// TestWasmP3ParamAsyncExport covers a WASI Preview-3 async export WITH parameters:
// `async function add(a: i32, b: i32): i32` lifted as `add: async func(a, b: u32)
// -> u32`. The wasmbin async wrapper now mirrors and forwards the source
// function's scalar parameters (it was `() -> ()`, hardcoded for no-param
// main/run), so the composer's param-aware lift
// (BuildAsyncLiftedExportComponentParams) wires them through. `--invoke
// 'add(20, 22)'` under wasmtime's async features returns 42. This is what
// unblocks authoring a param-taking async *provider* in Fern (rather than a
// hand-built core).
func TestWasmP3ParamAsyncExport(t *testing.T) {
	skipIfPreview2Missing(t)

	src := `async function add(a: i32, b: i32): i32 { return a + b; }
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
		AsyncSourceFunc: "add",
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	comp := component.BuildAsyncLiftedExportComponentParams(
		core, "__async_run", "add",
		[]string{"a", "b"}, []byte{component.CValtypeU32, component.CValtypeU32},
		component.CValtypeU32)

	p := filepath.Join(dir, "param_async_export.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "add(20, 22)", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (param async export): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("param async export add(20,22): got %q, want 42", bytes.TrimSpace(out))
	}
}
