// x86-64 native `remove_dir_all` (recursive `rm -rf`) coverage.
//
// Regression for issue #5372: std/test's TestRunner.finish()
// calls remove_dir_all(...) to clean up its temp dirs, so any TAP
// program that imports std/test references the builtin. The
// native x86-64 CLI pipeline had no lowering for it — neither a
// __fern_remove_dir_all runtime helper nor a call-target remap —
// so the in-process assembler failed with `undefined label
// "remove_dir_all"` and no examples/tests/*.fern would link
// natively. The fix ports arm64-ssa's emitRemoveDirAllHelper to
// the x86-64 backend (inlined openat/getdents64/unlinkat/close
// syscalls, self-recursion per directory entry).
//
// These tests exercise the helper end-to-end: a nested tree is
// fully removed (→ None), a missing path is a silent success
// (→ None, matching os.RemoveAll), and a plain file is unlinked
// via the ENOTDIR path (→ None). The interpreter is the oracle
// for the same shapes in interp_script_test.go; here we assert
// the native binary both links and produces the right filesystem
// effect.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/parser"
)

// compileRunX86_64WithSetup builds `src`, runs `setup(dir)` to
// populate the run directory, then executes the binary with its
// cwd set to that dir (so relative paths in the program resolve
// against it). Returns the exit code and the dir for host-side
// assertions about what the program deleted.
func compileRunX86_64WithSetup(t *testing.T, src string, setup func(dir string)) (int, string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	dir := t.TempDir()
	if setup != nil {
		setup(dir)
	}
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	cmd.Dir = dir
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode(), dir
}

// A nested tree (files at every level) is fully removed. Each
// child file drives a recursion that hits ENOTDIR and unlinks;
// each emptied directory is then rmdir'd on the way back up.
func TestX86_64RemoveDirAllNestedTree(t *testing.T) {
	src := `function main(): i32 {
    match (remove_dir_all("tree")) {
        Err(e) => { return 40; },
        Ok(_) => { return 0; }
    }
    return 0 - 1;
}`
	code, dir := compileRunX86_64WithSetup(t, src, func(dir string) {
		mkTree(t, dir)
	})
	if code != 0 {
		t.Errorf("exit = %d, want 0 (None — tree removed)", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "tree")); !os.IsNotExist(err) {
		t.Errorf("tree still exists after remove_dir_all (stat err = %v)", err)
	}
}

// remove_dir_all on a missing path is a silent success (Ok(())),
// mirroring os.RemoveAll — the ENOENT from openat maps to None.
func TestX86_64RemoveDirAllMissing(t *testing.T) {
	src := `function main(): i32 {
    match (remove_dir_all("no_such_dir")) {
        Err(e) => { return 1; },
        Ok(_) => { return 0; }
    }
    return 0 - 1;
}`
	code, _ := compileRunX86_64WithSetup(t, src, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 (None on a missing path)", code)
	}
}

// remove_dir_all on a plain file unlinks it (the ENOTDIR branch
// from openat with O_DIRECTORY) and returns None; the file is
// gone afterward.
func TestX86_64RemoveDirAllFile(t *testing.T) {
	src := `function main(): i32 {
    match (remove_dir_all("a_file.txt")) {
        Err(e) => { return 2; },
        Ok(_) => { return 0; }
    }
    return 0 - 1;
}`
	code, dir := compileRunX86_64WithSetup(t, src, func(dir string) {
		if err := os.WriteFile(filepath.Join(dir, "a_file.txt"), []byte("z"), 0o644); err != nil {
			t.Fatalf("seed file: %v", err)
		}
	})
	if code != 0 {
		t.Errorf("exit = %d, want 0 (None — file unlinked)", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "a_file.txt")); !os.IsNotExist(err) {
		t.Errorf("file still exists after remove_dir_all (stat err = %v)", err)
	}
}

// The issue's headline regression pin: an unmodified
// examples/tests TAP file must compile to a native x86-64 binary
// through the full CLI pipeline (modload + the in-process
// assembler) and run. Before the fix this failed at link with
// `undefined label "remove_dir_all"` — std/test's
// TestRunner.finish() references the builtin unconditionally via
// its temp-dir cleanup loop. We assert both that it links/runs
// and that the TAP output reports no failures.
func TestX86_64ArithmeticTapLinksNatively(t *testing.T) {
	_, runner := x86_64Tooling(t)
	fern := buildFernCLI(t)
	out := filepath.Join(t.TempDir(), "arith_tap")
	// Default -target x86-64 path = the in-process pure-Go
	// assembler+linker, exactly the pipeline the issue reports.
	if o, err := exec.Command(fern, "-target", "x86-64-linux", "-o", out,
		"../../examples/tests/arithmetic_test.fern").CombinedOutput(); err != nil {
		t.Fatalf("native compile of arithmetic_test.fern failed: %v\n%s", err, o)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(out)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], out)...)
	}
	tap, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("TAP binary exit = %d, want 0\n%s", code, tap)
	}
	if s := string(tap); !strings.Contains(s, "# fail 0") {
		t.Errorf("TAP output missing \"# fail 0\":\n%s", s)
	}
}

// mkTree builds dir/tree with files at every level and a couple
// of sibling subdirectories, so the removal exercises multi-entry
// directories and several recursion depths.
func mkTree(t *testing.T, dir string) {
	t.Helper()
	root := filepath.Join(dir, "tree")
	for _, d := range []string{
		root,
		filepath.Join(root, "a"),
		filepath.Join(root, "a", "b"),
		filepath.Join(root, "a", "b", "c"),
		filepath.Join(root, "sib"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	for _, f := range []string{
		filepath.Join(root, "f0.txt"),
		filepath.Join(root, "a", "f1.txt"),
		filepath.Join(root, "a", "b", "f2.txt"),
		filepath.Join(root, "a", "b", "c", "f3.txt"),
		filepath.Join(root, "sib", "f4.txt"),
	} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
}
