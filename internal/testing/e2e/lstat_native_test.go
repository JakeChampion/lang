package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func lstatProbeSource(file, linkToFile, linkToDir, dir string) string {
	return fmt.Sprintf(`function kind(p: string): i32 {
    match (lstat(p)) {
        Ok(s) => { if (s.is_dir) { return 1; } if (s.is_file) { return 2; } return 0; },
        Err(e) => { return 9; },
    }
    return 9;
}
function skind(p: string): i32 {
    match (stat(p)) {
        Ok(s) => { if (s.is_dir) { return 1; } if (s.is_file) { return 2; } return 0; },
        Err(e) => { return 9; },
    }
    return 9;
}
function main(): i32 {
    // A regular file is a regular file either way.
    if (kind(%[1]q) != 2) { return 1; }
    if (skind(%[1]q) != 2) { return 2; }
    // A symlink to a file: lstat sees the link, stat sees the file.
    if (kind(%[2]q) != 0) { return 3; }
    if (skind(%[2]q) != 2) { return 4; }
    // A symlink to a directory: lstat sees the link, stat sees the directory.
    if (kind(%[3]q) != 0) { return 5; }
    if (skind(%[3]q) != 1) { return 6; }
    // A real directory is a directory either way.
    if (kind(%[4]q) != 1) { return 7; }
    if (skind(%[4]q) != 1) { return 8; }
    // A missing path errors on both, rather than reporting "neither".
    if (kind(%[1]q + ".nope") != 9) { return 9; }
    if (skind(%[1]q + ".nope") != 9) { return 10; }
    return 0;
}
`, file, linkToFile, linkToDir, dir)
}

func lstatProbeTree(t *testing.T) (file, linkToFile, linkToDir, dir string) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, "realdir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file = filepath.Join(root, "real.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	linkToFile = filepath.Join(root, "link_to_file")
	if err := os.Symlink(file, linkToFile); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	linkToDir = filepath.Join(root, "link_to_dir")
	if err := os.Symlink(dir, linkToDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return file, linkToFile, linkToDir, dir
}

func TestX86_64Lstat(t *testing.T) {
	file, linkToFile, linkToDir, dir := lstatProbeTree(t)
	code, _ := compileRunX86_64WithSetup(t, lstatProbeSource(file, linkToFile, linkToDir, dir), nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 — see lstatProbeSource for which pair disagreed", code)
	}
}

func TestArm64Lstat(t *testing.T) {
	file, linkToFile, linkToDir, dir := lstatProbeTree(t)
	out, code := compileAndRunArm64(t, lstatProbeSource(file, linkToFile, linkToDir, dir))
	if code != 0 {
		t.Errorf("exit = %d, want 0 — see lstatProbeSource for which pair disagreed\n%s", code, out)
	}
}

func TestArm64DarwinLstat(t *testing.T) {
	file, linkToFile, linkToDir, dir := lstatProbeTree(t)
	buildAndRunDarwin(t, t.TempDir(), lstatProbeSource(file, linkToFile, linkToDir, dir))
}
