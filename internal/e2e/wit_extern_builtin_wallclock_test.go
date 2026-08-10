package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestExternImportWithBuiltinWallClockViaCLI pins that the world-driven
// composer wires the wall-clock `now()` builtin. An `@import` extern routes
// the whole module through ComposeFromWorldAuto, which classifies every
// import against the embedded fern world; `now_ns()` imports
// wasi:clocks/wall-clock@0.2.0::now, whose result is the `datetime` record
// referenced through a `(type (eq N))` re-export.
//
// Before the world decoder followed that eq-alias (ResolveDef in
// internal/wasm/componenttype), the datetime result resolved to nil and
// `now` was misclassified as a no-opt lowering — so the emitted `canon
// lower` dropped the required `memory` option and wasm-tools rejected the
// component with "canonical option `memory` is required". This is the
// regression gate for that fix (the analogue of the monotonic-clock `now`
// the world gained earlier — wall-clock returns a record, so it exercises
// the eq-aliased composite-result path specifically).
func TestExternImportWithBuiltinWallClockViaCLI(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}

	const want = "wall-clock-ok"
	src := `@import("wasi:random/random@0.2.0", "get-random-bytes")
function rand_bytes(n: u64): u8[];

function main(): i32 {
	var b: u8[] = rand_bytes(4 as u64);
	var t: i64 = now_ns();
	if (b.len() == 4 && t > 0) { write("` + want + `"); } else { write("bad"); }
	return 0;
}`
	progPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	compPath := filepath.Join(dir, "prog.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", compPath, progPath).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm: %v\n%s", err, out)
	}
	out, err := exec.Command(wasmtime, "run", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), want) {
		t.Fatalf("stdout = %q, want it to contain %q", out, want)
	}
}
