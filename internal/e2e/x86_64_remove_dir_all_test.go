// x86-64 `remove_dir_all` (recursive `rm -rf`) coverage.
//
// std/test's TestRunner.finish() calls remove_dir_all(...) to clean up its
// temp dirs, so any TAP program that imports std/test references the builtin
// (#5372). A nested tree is fully removed, a missing path is a silent success
// (matching os.RemoveAll), and a plain file is unlinked via the ENOTDIR path.
// The interpreter is the oracle for the same shapes in interp_script_test.go;
// here the binary must link and produce the right filesystem effect.
package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// compileRunX86_64WithSetup builds `src`, runs `setup(dir)` to
// populate the run directory, then executes the binary with its
// cwd set to that dir (so relative paths in the program resolve
// against it). Returns the exit code and the dir for host-side
// assertions about what the program deleted.
func compileRunX86_64WithSetup(t *testing.T, src string, setup func(dir string)) (int, string) {
	t.Helper()
	_, runner := x86_64Tooling(t)
	srcPath := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	binPath := e2eharness.CompileSelfHostFile(t, e2eharness.TargetX86_64Linux, srcPath, nil)
	dir := t.TempDir()
	if setup != nil {
		setup(dir)
	}
	cmd := e2eharness.RunX86_64Bin(runner, binPath)
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

// The issue's main regression pin: an unmodified examples/tests TAP file
// compiles to an x86-64 binary and runs. std/test's TestRunner.finish()
// references the builtin unconditionally via its temp-dir cleanup loop, so a
// missing lowering fails the link. Asserts both that it runs and that the TAP
// output reports no failures.
func TestX86_64ArithmeticTapLinks(t *testing.T) {
	_, runner := x86_64Tooling(t)
	out := filepath.Join(t.TempDir(), "arith_tap")
	if o, err := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetX86_64Linux, "../../examples/tests/arithmetic_test.fern", out).CombinedOutput(); err != nil {
		t.Fatalf("compile of arithmetic_test.fern failed: %v\n%s", err, o)
	}
	cmd := e2eharness.RunX86_64Bin(runner, out)
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
