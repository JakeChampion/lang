package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// lstatProbeSource builds a program that classifies four paths through both
// `stat` and `lstat` and reports the pair as one digit each.
//
// The pairs are the whole point. `stat` and `lstat` differ on exactly one input
// — a symlink — and a test that only ran `lstat` would pass just as well
// against an `lstat` that quietly followed links, since a link to a regular
// file is a regular file. Asserting `stat` alongside it makes the difference
// itself the assertion.
//
// 2 = regular file, 1 = directory, 0 = neither (a symlink under lstat), 9 = an
// error. A symlink's S_IFMT is S_IFLNK, which is neither S_IFREG nor S_IFDIR,
// so both FileStat flags come back false — the same three-way answer
// fs.DirEntry.Type() gives a directory walk.
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

// lstatProbeTree writes the four paths the probe classifies and returns them.
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

// lstat is the answer to "is this entry a symlink" that Fern had no way to ask
// (#7982): `stat` follows links and FileStat carries no link bit, so
// `internal/embed`'s `!d.Type().IsRegular()` skip — the thing that keeps an
// asset tree from reaching outside its root or wedging a walk on a cycle — had
// no spelling in the language and so none in the self-host compiler either.
//
// Every backend implements it as its existing stat helper with one constant
// changed: newfstatat/fstatat's flags word gains AT_SYMLINK_NOFOLLOW, and
// preview-1's path_filestat_get loses its symlink_follow lookupflag. That is
// why these tests compare the two builtins rather than checking lstat alone —
// the shared body means a copy-paste slip shows up as the two agreeing.
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
