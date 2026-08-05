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

// TestWasmP3AsyncImportPendingFromFern is the colorless async-import vertical's
// PENDING payoff from REAL Fern source: unlike TestWasmP3AsyncImportFromFern
// (whose provider completes synchronously, so the `canon lower async` returns
// RETURNED and the wrapper's await loop is skipped), here the bundled provider
// GENUINELY DEFERS — its core `thread.yield`s before `task.return` — so the
// consumer's lower returns a STARTED status and the wasmbin-generated wrapper's
// pending-await loop (waitable-set.new → waitable.join → waitable-set.wait →
// subtask.drop → waitable-set.drop, then read the return area) actually
// executes. The waitable intrinsics the loop calls are imported under "" and
// satisfied by BuildAsyncImportsAwaitComponent (ws-wait trampolined over the
// consumer's memory, the rest direct canon funcs). Running `run()` under
// wasmtime's async features returns the provider's value (42) — proving real
// Fern `@import async` of a host-deferring function drives the full pending
// await path end to end, the last async-import ABI capability. See
// docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func TestWasmP3AsyncImportPendingFromFern(t *testing.T) {
	skipIfPreview2Missing(t) // ensures wasmtime on PATH

	src := `@import("test:dep/d", "compute") async function dep(): i32;
async function run(): i32 { return dep(); }
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

	// The bundled provider DEFERS: p3PendProviderCore thread.yields before
	// task-returning 42, exported as `compute: async func() -> u32`.
	provider := component.BuildPendingDeferringProviderComponent(p3PendProviderCore, "dep", "compute", component.CValtypeU32)

	// The async-lowered import is `(retptr) -> status` = (i32) -> (i32).
	comp := component.BuildAsyncImportAwaitComponent(
		core, "test:dep/d", "compute",
		provider, "compute",
		"__async_run", "run",
		[]byte{encode.ValtypeI32}, []byte{encode.ValtypeI32},
		component.CValtypeU32,
	)

	p := filepath.Join(dir, "fern_async_pending.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async import pending from Fern): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("Fern async import (pending): got %q, want 42", bytes.TrimSpace(out))
	}
}
