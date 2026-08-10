package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestExternImportScalarRunsUnderWasmtime is P4b's end-to-end gate
// (bring-your-own WIT, docs/WIT-BRING-YOUR-OWN.md): a Fern program declares a
// scalar WASI import via `@import` on a body-less function and calls it. The
// call must lower to a real core wasm function import of the declared
// (interface, wit-name) — proven by the core module carrying that import —
// and the world-driven composer must wire it so the component validates and
// runs under wasmtime.
//
// `wasi:random/random@0.2.0` `get-random-u64: func() -> u64` is the canonical
// scalar extern: no params, a single u64 result, no memory. It is declared by
// the embedded `fern` world, so ComposeFromWorldAuto classifies and wires the
// core import with no hardcoded list.
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

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}

	// `r & 0` is deterministically 0, so the program prints a fixed string and
	// exits 0 — but the random_u64() result flows into the branch condition,
	// so the call (and thus the import) survives optimisation.
	const want = "extern-import-ok"
	src := `@import("wasi:random/random@0.2.0", "get-random-u64")
function random_u64(): u64;

function main(): i32 {
	var r: u64 = random_u64();
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
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", refPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm: %v\n%s", err, out)
	}
	ref, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	core := componentCoreSection(t, ref)

	// Codegen gate: the `@import` lowered to a concrete core wasm function
	// import of the declared (interface, wit-name).
	if !bytes.Contains(core, []byte("wasi:random/random@0.2.0")) {
		t.Errorf("core module is missing the imported interface %q", "wasi:random/random@0.2.0")
	}
	if !bytes.Contains(core, []byte("get-random-u64")) {
		t.Errorf("core module is missing the imported function %q", "get-random-u64")
	}

	// Compose via the world-driven path: the wired imports are derived from
	// the core's own imports, classified against the embedded fern world.
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
