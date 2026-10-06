package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TrailingSlashSource exercises the rule that a path ending in '/' names a
// directory or nothing (POSIX). Linux's kernel refuses `link-to-file/`; XNU
// opens the file, so the Darwin runtime refuses it itself, as gnulib's open
// does for GNU: ENOTDIR for a read, EISDIR for a write or create, before
// anything is truncated (#11430).
const TrailingSlashSource = `function why(e: IoError): string {
    match (e) { Other(_, m) => { return m; }, NotFound(_) => { return "NotFound"; }, _ => { return "other"; } }
}
function main(): i32 {
    match (open_reader("lf/")) { Ok(_) => { print("open_reader ok"); }, Err(e) => { print("open_reader " + why(e)); } }
    match (read_file("lf/")) { Ok(_) => { print("read_file ok"); }, Err(e) => { print("read_file " + why(e)); } }
    match (read_file_bytes("lf/")) { Ok(_) => { print("read_file_bytes ok"); }, Err(e) => { print("read_file_bytes " + why(e)); } }
    match (write_file("lf/", "x")) { Ok(_) => { print("write_file ok"); }, Err(e) => { print("write_file " + why(e)); } }
    match (open_writer("lf/")) { Ok(_) => { print("open_writer ok"); }, Err(e) => { print("open_writer " + why(e)); } }
    match (open_reader("d/")) { Ok(_) => { print("dir ok"); }, Err(e) => { print("dir " + why(e)); } }
    match (read_file("f")) { Ok(s) => { print("f " + s); }, Err(e) => { print("f " + why(e)); } }
    return 0;
}
`

const trailingSlashWant = `open_reader Not a directory
read_file Not a directory
read_file_bytes Not a directory
write_file Is a directory
open_writer Is a directory
dir ok
f hello
`

// TrailingSlashTree is a file f holding "hello", a symlink lf to it and a
// directory d.
func TrailingSlashTree(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("f", filepath.Join(dir, "lf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// CheckTrailingSlash runs `run` in the tree `dir` and holds it to the Linux
// kernel's answers, with the file left untouched by the refused writes.
func CheckTrailingSlash(t testing.TB, run *exec.Cmd, dir string) {
	t.Helper()
	run.Dir = dir
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if string(out) != trailingSlashWant {
		t.Fatalf("got:\n%s\nwant:\n%s", out, trailingSlashWant)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "f")); err != nil || string(b) != "hello" {
		t.Fatalf("f after the writes = %q, %v; want it untouched", b, err)
	}
}
