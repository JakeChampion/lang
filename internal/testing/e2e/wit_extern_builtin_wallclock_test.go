package e2e

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExternImportWithBuiltinWallClockViaCLI(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()

	const want = "wall-clock-ok"
	src := `@import("wasi:random/random@0.2.0", "get-random-bytes")
function rand_bytes(n: u64): u8[];

function main(): i32 {
	let b: u8[] = rand_bytes(4 as u64);
	let t: i64 = now_ns();
	if (b.len() == 4 && t > 0) { write("` + want + `"); } else { write("bad"); }
	return 0;
}`
	progPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(progPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write prog: %v", err)
	}
	compPath := filepath.Join(dir, "prog.wasm")
	if out, err := e2eharness.SelfHostCompileCmd(t, "wasm32-wasi", progPath, compPath).CombinedOutput(); err != nil {
		t.Fatalf("self-host fern -target wasm32-wasi: %v\n%s", err, out)
	}
	out, err := exec.Command(wasmtime, "run", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), want) {
		t.Fatalf("stdout = %q, want it to contain %q", out, want)
	}
}
