package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The self-host's WAT → binary assembler (examples/self_host/watbin.fern)
// wrote every global's type as i32, whatever the text said. An i64 global then
// produced a module whose declared global type disagreed with its own
// `i64.const` initialiser, and the loader rejected the whole module — "type
// mismatch: expected i32, found i64" — before a single instruction ran.
//
// Two i64 globals reach that path: the leak census's counters under
// FERN_LEAKCHECK, and the allocation count of #9596. So the bug hid where
// nobody looked: the WAT the compiler printed was correct and validated, only
// the binary it assembled from that WAT was wrong, and neither observable is
// on by default.
//
// This runs the shape that has no env var to forget: a program reading
// `__heap_alloc_count()`, compiled to wasm by the self-host compiler and
// executed. It exits with the count it observed, so a module that loads but
// mis-encodes the global fails here too.
func TestSelfHostWasmI64GlobalEncodesItsOwnType(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm i64-global check")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the compiler binary runs natively here (it writes the module by path)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	// Three allocations, then the count: each `+` builds a fresh string, so
	// the count has to have moved. The verdict goes to stdout rather than the
	// exit code, because `wasmtime run` reports every non-zero program exit as
	// 1 and would flatten "counted" and "blind" into the same reading.
	const src = `function main(): i32 {
	var a: string = "x" + "y";
	var b: string = a + "z";
	var c: string = b + "!";
	if (c.len() != 4) { print("wrong-length"); return 0; }
	if (__heap_alloc_count() > (0 as i64)) { print("counted"); } else { print("blind"); }
	return 0;
}
`
	srcPath := filepath.Join(dir, "count.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	modPath := filepath.Join(dir, "count.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", srcPath, stdlibRoot, "-o", modPath).CombinedOutput(); err != nil {
		t.Fatalf("self-host wasm compile: %v\n%s", err, out)
	}
	cmd := exec.Command("wasmtime", "run", modPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run: %v — a load error here means the global's type was mis-encoded\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "counted" {
		t.Fatalf("module printed %q, want \"counted\"", got)
	}
}
