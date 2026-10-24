package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostRemoveDirAllIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	rda := filepath.Join(dir, "rda")
	for _, d := range []string{filepath.Join(rda, "sub", "sub2")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	for _, f := range []string{
		filepath.Join(rda, "f1.txt"), filepath.Join(rda, "f2.txt"),
		filepath.Join(rda, "sub", "g.txt"), filepath.Join(rda, "sub", "sub2", "h.txt"),
	} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	src := fmt.Sprintf(`function main(): i32 {
    match (remove_dir_all(%q)) {
        Err(_) => { return 1; },
        Ok(_) => {
            match (remove_dir_all(%q)) {
                Err(_) => { return 2; },
                Ok(_) => { return 0; },
            }
        },
    }
}`, rda, rda+"-missing")

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
	if !bytes.Contains(asm, []byte("call __fn___fern_remove_dir_all")) {
		t.Fatal("remove_dir_all did not reach the Fern IR runtime (no call __fn___fern_remove_dir_all in asm)")
	}
	progBin := buildBin(t, gcc, dir, "rda_prog", string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("remove_dir_all IR program exited %d, want 0 (nested tree removed -> None, missing -> None)", code)
	}
	if _, err := os.Stat(rda); !os.IsNotExist(err) {
		t.Errorf("rda still present after remove_dir_all (stat err = %v)", err)
	}
}

func TestSelfHostRemoveDirAllIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host remove_dir_all wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	rda := filepath.Join(dir, "rda_dir")
	mustMk := func(p string) {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
	mustWr := func(p string) {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	mustMk(filepath.Join(rda, "sub", "sub2"))
	mustWr(filepath.Join(rda, "f1.txt"))
	mustWr(filepath.Join(rda, "f2.txt"))
	mustWr(filepath.Join(rda, "sub", "g.txt"))
	mustWr(filepath.Join(rda, "sub", "sub2", "h.txt"))

	const src = `function main(): i32 {
    match (remove_dir_all("rda_dir")) {
        Err(_) => { return 1; },
        Ok(_) => {
            match (remove_dir_all("rda_missing")) {
                Err(_) => { return 2; },
                Ok(_) => { return 0; },
            }
        },
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
	if !bytes.Contains(wat, []byte("call $__fern_remove_dir_all")) {
		t.Fatal("remove_dir_all did not reach the wasm IR runtime path (no call $__fern_remove_dir_all in WAT)")
	}
	watFile := filepath.Join(dir, "rda_prog.wat")
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
		t.Errorf("remove_dir_all wasm IR program exited %d, want 0 (tree removed -> None, missing -> None)\n--- WAT ---\n%s", code, wat)
	}
	if _, err := os.Stat(rda); !os.IsNotExist(err) {
		t.Errorf("rda_dir still present after remove_dir_all (stat err = %v)", err)
	}
}
