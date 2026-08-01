package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostRemoveFileIR pins `remove_file(path)` lowering on the self-host
// x86-64 IR path. remove_file unlinks a file and returns Option[IoError]; it had
// a full AST runtime (__fern_remove_file) but no IR lowering, so it bailed
// `BAIL call[remove_file]` -> AST (#3457: filesystem_ops). It now lowers to
// op_remove_file -> the same __fern_remove_file runtime the AST path calls. The
// program makes a temp dir, writes a file, remove_file's it, then read_dir's the
// dir and asserts it's empty (0 entries) -> exit 0; exercises temp_dir /
// write_file / remove_file / read_dir / remove_dir_all on the IR path.
func TestSelfHostRemoveFileIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    match (temp_dir("fern-rmfile-ir")) {
        Ok(d) => {
            match (write_file(d + "/f.txt", "x")) { Some(_) => { return 1; }, None => {}, }
            match (remove_file(d + "/f.txt")) { Some(_) => { return 2; }, None => {}, }
            match (read_dir(d)) {
                Ok(names) => {
                    var n: i32 = names.len();
                    match (remove_dir_all(d)) { Some(_) => { return 3; }, None => {}, }
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
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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

// TestSelfHostRemoveFileIRWasm is the wasm mirror: remove_file now lowers through
// the wasm IR path too (it was a wasm_eligible exclusion). The wasm op_remove_file
// handler calls a fresh runtime ($__fern_remove_file: preview1 path_unlink_file ->
// Option[IoError] box [tag@0, payload@4], None=1 on success / Some=0 on failure
// with the same null-IoError placeholder $__fern_stat's Err arm uses — the match
// binds it with a wildcard). Unlike the x86 test this exercises remove_file alone
// (temp_dir / read_dir are not yet wasm-eligible): it removes a known file (None)
// and a missing file (Some) under wasmtime with the temp dir as preopen, exiting 0
// only if both resolve, and the test confirms the known file is gone on disk.
func TestSelfHostRemoveFileIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host remove_file wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	// A known regular file under the temp dir — remove_file should unlink it.
	target := filepath.Join(dir, "rmtarget.txt")
	if err := os.WriteFile(target, []byte("delete me\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	// Relative paths resolved against the preopen (the temp dir, mapped to /).
	const src = `function main(): i32 {
    match (remove_file("rmtarget.txt")) {
        None => {
            match (remove_file("does_not_exist.txt")) {
                Some(_) => { return 0; },
                None => { return 1; },
            }
        },
        Some(_) => { return 2; },
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
	// The known file must actually be gone (the None path really unlinked it).
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("rmtarget.txt still present after remove_file (stat err = %v)", err)
	}
}
