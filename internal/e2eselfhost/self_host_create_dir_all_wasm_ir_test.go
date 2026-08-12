package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostCreateDirAllIRWasm pins `create_dir_all(path)` on the wasm IR
// path (#6749) — the builtin that lets a Fern program BUILD a directory tree,
// which is what `fern -vendor` is gated on.
//
// WASI takes an explicit path length, so $__fern_create_dir_all walks the chain
// by calling path_create_directory with a shorter length per component rather
// than rewriting separators the way the native helpers do. That divergence is
// what this pins: the program creates a two-deep chain under the preopen and
// writes into the leaf, so a walk that created only the last component (or
// mis-sliced a prefix) fails at the write rather than silently passing.
func TestSelfHostCreateDirAllIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host create_dir_all wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    match (create_dir_all("vendor/pkg/src")) { Err(_) => { return 1; }, Ok(_) => {}, }
    match (write_file("vendor/pkg/src/lib.fern", "hello")) { Err(_) => { return 2; }, Ok(_) => {}, }
    match (read_file("vendor/pkg/src/lib.fern")) {
        Ok(s) => { if (s != "hello") { return 3; } },
        Err(_) => { return 4; },
    }
    match (create_dir_all("vendor/pkg/src")) { Err(_) => { return 5; }, Ok(_) => {}, }
    return 0;
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("call $__fern_create_dir_all")) {
		t.Fatal("create_dir_all did not reach the wasm IR runtime path (no call $__fern_create_dir_all in WAT)")
	}
	watFile := filepath.Join(dir, "cda_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
	run.Dir = dir
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("create_dir_all wasm IR program exited %d, want 0\n--- WAT ---\n%s", code, wat)
	}
	if fi, err := os.Stat(filepath.Join(dir, "vendor", "pkg", "src")); err != nil || !fi.IsDir() {
		t.Errorf("vendor/pkg/src is not a directory under the preopen (err = %v)", err)
	}
}
