package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestExternImportViaCLI is the end-user gate for bring-your-own WIT
// (docs/WIT-BRING-YOUR-OWN.md): a program that declares `@import` externs must
// compile straight through the real `fern -target wasm` CLI — not just the
// test harness — and run under wasmtime. The CLI's legacy composer doesn't
// know arbitrary WIT imports, so the presence of any extern routes the whole
// module through the world-driven composer (ComposeFromWorldAuto).
//
// The program mixes a scalar extern (get-random-u64) and a composite-result
// one (get-random-bytes -> u8[]), plus a built-in write, so the world-driven
// path has to wire all three uniformly.
func TestExternImportViaCLI(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()

	fernBin := filepath.Join(dir, "fern")
	if out, err := exec.Command("go", "build", "-o", fernBin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, out)
	}

	const want = "cli-extern-ok"
	src := `@import("wasi:random/random@0.2.0", "get-random-bytes")
function rand_bytes(n: u64): u8[];

@import("wasi:random/random@0.2.0", "get-random-u64")
function rand_u64(): u64;

function main(): i32 {
	var a: u8[] = rand_bytes(16 as u64);
	var r: u64 = rand_u64();
	if (a.len() == 16 && (r & 0) == 0) { write("` + want + `"); } else { write("cli-extern-bad"); }
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
