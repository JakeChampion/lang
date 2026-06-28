package e2e

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
// to op_stat -> the same __fern_stat runtime the AST path calls (x86 transcribed,
// with FileStat pre-interned via shape_ref before the literal pool; arm64 reuses
// asm_arm64's heap-block runtime).
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
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
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
