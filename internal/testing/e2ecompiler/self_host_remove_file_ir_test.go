package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostRemoveFileIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    match (temp_dir("fern-rmfile-ir")) {
        Ok(d) => {
            match (write_file(d + "/f.txt", "x")) { Err(_) => { return 1; }, Ok(_) => {}, }
            match (remove_file(d + "/f.txt")) { Err(_) => { return 2; }, Ok(_) => {}, }
            match (read_dir(d)) {
                Ok(names) => {
                    let n: i32 = names.len();
                    match (remove_dir_all(d)) { Err(_) => { return 3; }, Ok(_) => {}, }
                    if (n != 0) { return 4; }
                    return 0;
                },
                Err(_) => { return 5; },
            }
        },
        Err(_) => { return 6; },
    }
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !strings.Contains(string(asm), "__fern_remove_file") {
		t.Fatal("remove_file did not reach the IR runtime path (no __fern_remove_file in asm)")
	}
	progBin := buildBin(t, gcc, dir, "rmfile_prog", string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("remove_file IR program exited %d, want 0 (write + remove_file + read_dir(==0))", code)
	}
}

func TestSelfHostRemoveFileIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host remove_file wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	target := filepath.Join(dir, "rmtarget.txt")
	if err := os.WriteFile(target, []byte("delete me\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	const src = `function main(): i32 {
    match (remove_file("rmtarget.txt")) {
        Ok(_) => {
            match (remove_file("does_not_exist.txt")) {
                Err(_) => { return 0; },
                Ok(_) => { return 1; },
            }
        },
        Err(_) => { return 2; },
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
	if !bytes.Contains(wat, []byte("call $__fern_remove_file")) {
		t.Fatal("remove_file did not reach the wasm IR runtime path (no call $__fern_remove_file in WAT)")
	}
	watFile := filepath.Join(dir, "rm_prog.wat")
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
		t.Errorf("remove_file wasm IR program exited %d, want 0 (known file -> None, missing -> Some)\n--- WAT ---\n%s", code, wat)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("rmtarget.txt still present after remove_file (stat err = %v)", err)
	}
}
