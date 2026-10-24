package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

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

func TestX86_64ArithmeticTapLinks(t *testing.T) {
	_, runner := x86_64Tooling(t)
	out := filepath.Join(t.TempDir(), "arith_tap")
	if o, err := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetX86_64Linux, "../../../tests/stdlib/arithmetic_test.fern", out).CombinedOutput(); err != nil {
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
