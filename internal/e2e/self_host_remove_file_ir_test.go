package e2e

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
