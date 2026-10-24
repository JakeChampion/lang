package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

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

	copySelfHostDriver(t, dir, "drivers/wasm_runio_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_runio_run.fern", "wasm_runio_run")

	const want = "self-host-extern-ok"
	prog := `@import("wasi:random/random@0.2.0", "get-random-u64")
function random_u64(): i64;
function main(): i32 {
    let r: i64 = random_u64();
    write("` + want + `");
    return 0;
}`
	watBytes := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(watBytes) == 0 {
		t.Fatal("self-host wasm emitter produced 0 bytes")
	}

	if !bytes.Contains(watBytes, []byte("wasi:random/random@0.2.0")) {
		t.Errorf("emitted core is missing the imported interface")
	}
	if !bytes.Contains(watBytes, []byte("get-random-u64")) {
		t.Errorf("emitted core is missing the imported function")
	}

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
