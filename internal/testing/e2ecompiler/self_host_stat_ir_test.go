package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostStatIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("stat IR test runs only natively (stats host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	filePath := filepath.Join(dir, "stat_target.txt")
	const fileBytes = "hello, stat!\n"
	if err := os.WriteFile(filePath, []byte(fileBytes), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	subDir := filepath.Join(dir, "stat_subdir")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	missing := filepath.Join(dir, "stat_does_not_exist")

	src := fmt.Sprintf(`function main(): i32 {
    match (stat(%q)) {
        Ok(s) => {
            if (!s.is_file) { return 1; }
            if (s.is_dir) { return 2; }
            if (s.size != %d) { return 3; }
            match (stat(%q)) {
                Ok(d) => {
                    if (!d.is_dir) { return 4; }
                    if (d.is_file) { return 5; }
                    match (stat(%q)) {
                        Ok(_) => { return 6; },
                        Err(_) => { return 0; },
                    }
                },
                Err(_) => { return 7; },
            }
        },
        Err(_) => { return 8; },
    }
}`, filePath, len(fileBytes), subDir, missing)

	cmd := exec.Command(driverBin)
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !strings.Contains(string(asm), "__fern_stat") {
		t.Fatal("stat did not reach the IR runtime path (no __fern_stat in asm)")
	}
	progBin := buildBin(t, gcc, dir, "stat_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("stat IR program exited %d, want 0 (file is_file+size / dir is_dir / missing Err)", code)
	}
}

func TestSelfHostStatIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host stat wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	const fileBytes = "hello, stat!\n"
	if err := os.WriteFile(filepath.Join(dir, "stat_target.txt"), []byte(fileBytes), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "stat_subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	src := fmt.Sprintf(`function main(): i32 {
    match (stat("stat_target.txt")) {
        Ok(s) => {
            if (!s.is_file) { return 1; }
            if (s.is_dir) { return 2; }
            if (s.size != %d) { return 3; }
            match (stat("stat_subdir")) {
                Ok(d) => {
                    if (!d.is_dir) { return 4; }
                    if (d.is_file) { return 5; }
                    match (stat("stat_does_not_exist")) {
                        Ok(_) => { return 6; },
                        Err(_) => { return 0; },
                    }
                },
                Err(_) => { return 7; },
            }
        },
        Err(_) => { return 8; },
    }
}`, len(fileBytes))

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
	if !bytes.Contains(wat, []byte("call $__fern_stat")) {
		t.Fatal("stat did not reach the wasm IR runtime path (no call $__fern_stat in WAT)")
	}
	watFile := filepath.Join(dir, "stat_prog.wat")
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
		t.Errorf("stat wasm IR program exited %d, want 0 (file is_file+size / dir is_dir / missing Err)\n--- WAT ---\n%s", code, wat)
	}
}
