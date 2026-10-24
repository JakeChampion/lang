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

func TestExternImportScalarRunsUnderWasmtime(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	dir := t.TempDir()

	const want = "extern-import-ok"
	src := `@import("wasi:random/random@0.2.0", "get-random-u64")
function random_u64(): u64;

function main(): i32 {
	let r: u64 = random_u64();
	if ((r & 0) == 0) {
		write("` + want + `");
	}
	return 0;
}`
	progPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	refPath := filepath.Join(dir, "ref.wasm")
	if out, err := e2eharness.SelfHostCompileCmd(t, "wasm32-wasi", progPath, refPath).CombinedOutput(); err != nil {
		t.Fatalf("self-host fern -target wasm32-wasi: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	core := componentCoreSection(t, ref)

	if !bytes.Contains(core, []byte("wasi:random/random@0.2.0")) {
		t.Errorf("core module is missing the imported interface %q", "wasi:random/random@0.2.0")
	}
	if !bytes.Contains(core, []byte("get-random-u64")) {
		t.Errorf("core module is missing the imported function %q", "get-random-u64")
	}

	w, err := componenttype.DecodeWorld("fern")
	if err != nil {
		t.Fatalf("DecodeWorld: %v", err)
	}
	comp, err := component.ComposeFromWorldAuto(core, w)
	if err != nil {
		t.Fatalf("ComposeFromWorldAuto: %v", err)
	}
	mine := filepath.Join(dir, "extern-random.wasm")
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
