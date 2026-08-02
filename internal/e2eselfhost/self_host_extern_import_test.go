package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestSelfHostExternImportRunsUnderWasmtime is the self-host P4b gate
// (bring-your-own WIT, docs/WIT-BRING-YOUR-OWN.md): the self-hosted wasm
// backend lowers an `@import` extern call to a real core wasm function import
// of the declared (interface, wit-name), and the world-driven composer wires
// it into a component that validates and runs under wasmtime. The self-host
// mirror of TestExternImportScalarRunsUnderWasmtime (the Go P4b gate).
//
// The program declares `wasi:random/random@0.2.0` `get-random-u64` (a scalar
// `() -> i64` extern, no memory) and calls it, then writes a fixed string and
// exits 0. The composition path is the Go ComposeFromWorldAuto against the
// embedded fern world (the self-host composer has its own gates); what's under
// test here is the self-host *codegen* of the extern import + call.
func TestSelfHostExternImportRunsUnderWasmtime(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()

	// Stage the self-host front end + wasm backend, plus a component-io driver
	// that emits the run core (emit_module_run_io) from source on stdin.
	copySelfHostDriver(t, dir, "wasm_runio_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_runio_run.fern", "wasm_runio_run")

	const want = "self-host-extern-ok"
	prog := `@import("wasi:random/random@0.2.0", "get-random-u64")
function random_u64(): i64;
function main(): i32 {
    var r: i64 = random_u64();
    write("` + want + `");
    return 0;
}`
	watBytes := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(watBytes) == 0 {
		t.Fatal("self-host wasm emitter produced 0 bytes")
	}

	// Codegen gate: the extern lowered to a concrete core wasm import of the
	// declared (interface, wit-name).
	if !bytes.Contains(watBytes, []byte("wasi:random/random@0.2.0")) {
		t.Errorf("emitted core is missing the imported interface")
	}
	if !bytes.Contains(watBytes, []byte("get-random-u64")) {
		t.Errorf("emitted core is missing the imported function")
	}

	// Assemble the WAT to a core module, then compose against the fern world.
	watPath := filepath.Join(dir, "core.wat")
	if err := os.WriteFile(watPath, watBytes, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	corePath := filepath.Join(dir, "core.wasm")
	if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse: %v\n%s", err, out)
	}
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatalf("read core: %v", err)
	}
	w, err := componenttype.DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	comp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto: %v", err)
	}
	mine := filepath.Join(dir, "extern.component.wasm")
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
	if string(out) != want {
		t.Errorf("stdout = %q, want %q", string(out), want)
	}
}
