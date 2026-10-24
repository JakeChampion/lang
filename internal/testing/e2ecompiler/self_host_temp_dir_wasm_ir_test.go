package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostTempDirIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host temp_dir wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

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
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
