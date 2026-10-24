package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

	const src = `function main(): i32 {
	let a: string = "x" + "y";
	let b: string = a + "z";
	let c: string = b + "!";
	if (c.len() != 4) { print("wrong-length"); return 0; }
	if (__heap_alloc_count() > (0 as i64)) { print("counted"); } else { print("blind"); }
	return 0;
}
`
	srcPath := filepath.Join(dir, "count.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	modPath := filepath.Join(dir, "count.wasm")
	if out, err := exec.Command(fernBin, "-target", "wasm32-wasi", "-o", modPath, srcPath, stdlibRoot).CombinedOutput(); err != nil {
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
