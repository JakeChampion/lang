package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStatIR pins `stat(path)` lowering on the self-host x86-64 IR path.
// stat returns Result[FileStat, IoError] — the Ok payload is the injected
// FileStat STRUCT (is_file / is_dir / size). It had a full AST runtime
// (__fern_stat) but no IR lowering, so a program using it bailed `BAIL call[stat]`
// -> AST, dragging the `batch7` test module (std/test's assert_is_file /
// assert_is_dir / assert_file_size) to the legacy emitter (#3457). It now lowers
// to op_stat -> __fn___fern_stat, which on x86-64 is now a Fern runtime function
// (#2649: asmcore.rt_src_stat — fstatat via __syscall4 into __raw_scratch, then
// build Ok(FileStat{...}) / Err(NotFound(_))). arm64 runs the SAME Fern body now;
// the FileStat shape is still pre-interned via shape_ref on both.
// `Contains(asm, "__fern_stat")` holds either way — __fn___fern_stat contains it
// as a substring — so it is not a guard against a revert; the lock-in tests in
// self_host_runtime_fern_helper_*_test.go are.
//
// This is the first struct-RESULT builtin on the IR path: the match arm
// `Ok(s) => s.is_file` binds `s` as a FileStat struct and the field reads resolve
// against its injected layout. The program stats a known regular file (asserting
// is_file + the exact byte size), a directory (is_dir), and a nonexistent path
// (Err), exiting 0 only if all three resolve correctly.
func TestSelfHostStatIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("stat IR test runs only natively (stats host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	// A known regular file (13 bytes) + a directory, both under the temp dir.
	filePath := filepath.Join(dir, "stat_target.txt")
	const fileBytes = "hello, stat!\n" // 13 bytes
	if err := os.WriteFile(filePath, []byte(fileBytes), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	subDir := filepath.Join(dir, "stat_subdir")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	missing := filepath.Join(dir, "stat_does_not_exist")

	// Embed the absolute paths into the program source. The match binds the Ok
	// payload `s` as FileStat; s.is_file / s.is_dir / s.size read its fields.
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

	cmd := exec.Command(driverBin, "-ir")
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

// TestSelfHostStatIRWasm is the wasm mirror: stat now lowers through the wasm IR
// path too (it was a wasm_eligible exclusion). The wasm op_stat handler builds
// the Result[FileStat, IoError] box INLINE — it pushes FileStat's module-specific
// struct type-id and calls $__fern_stat (a fresh wasm runtime: preview1
// path_filestat_get -> the FileStat struct box [type_id@0, is_file@8, is_dir@16,
// size@24] wrapped Ok, or Result.Err on failure). The first struct-RESULT builtin
// on the wasm IR path. Run under wasmtime with the temp dir as preopen (`--dir=.::/`,
// CWD = temp dir) so the relative paths resolve; the program exits 0 only if the
// regular file (is_file + 13-byte size), the directory (is_dir), and the missing
// path (Err) all resolve correctly.
func TestSelfHostStatIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host stat wasm IR e2e")
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
	const fileBytes = "hello, stat!\n" // 13 bytes
	if err := os.WriteFile(filepath.Join(dir, "stat_target.txt"), []byte(fileBytes), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "stat_subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	// Relative paths — resolved against the preopen (the temp dir, mapped to guest /).
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
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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
