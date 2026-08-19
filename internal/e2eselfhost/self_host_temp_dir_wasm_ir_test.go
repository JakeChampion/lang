package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostTempDirIRWasm pins `temp_dir(prefix)` on the wasm IR path. temp_dir
// creates a uniquely-named directory and returns Result[string, IoError]; it was a
// wasm_eligible exclusion. It now lowers to op_temp_dir -> $__fern_temp_dir, a
// fresh runtime that builds `<prefix>-<monotonic_ns>` (ns rendered to decimal
// inline), path_create_directorys it under the preopen, and boxes Ok(name) /
// Err(IoError). Unlike asm_ir's __fern_temp_dir it drops the absolute "/tmp/"
// prefix — wasm has no global /tmp, only the preopen, so the returned name is
// preopen-RELATIVE, the same model every other wasm fs op uses (so the name
// round-trips through stat / read_dir / remove_dir_all within the sandbox).
//
// Value-tested under wasmtime with the temp dir as preopen (`--dir=.::/`, CWD =
// temp dir): the program creates a temp dir, checks the returned path is non-empty
// and starts with the requested prefix, and exits 0; the Go side then confirms a
// matching directory was actually created on disk. The test also pins that the IR
// path was taken (`call $__fern_temp_dir` in the WAT).
func TestSelfHostTempDirIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host temp_dir wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	// "fern-td-ir" is 10 bytes; the result is "<prefix>-<ns>", so >= 12 bytes, and
	// d[0] == 'f' (102). Exit 0 only if the path looks right.
	const src = `function main(): i32 {
    match (temp_dir("fern-td-ir")) {
        Ok(d) => {
            if (d.len() < 12) { return 1; }
            if (d[0] != 102) { return 2; }
            return 0;
        },
        Err(_) => { return 3; },
    }
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
	if !bytes.Contains(wat, []byte("call $__fern_temp_dir")) {
		t.Fatal("temp_dir did not reach the wasm IR runtime path (no call $__fern_temp_dir in WAT)")
	}
	watFile := filepath.Join(dir, "td_prog.wat")
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
		t.Errorf("temp_dir wasm IR program exited %d, want 0 (Ok path non-empty + prefix)\n--- WAT ---\n%s", code, wat)
	}
	// A directory named "fern-td-ir-<ns>" must have been created under the preopen.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir temp dir: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "fern-td-ir-") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("temp_dir did not create a fern-td-ir-* directory under the preopen")
	}
}
