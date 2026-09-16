package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// A second `-o` to the same path must replace the executable, not rewrite it
// through its inode. macOS caches the code-signature verdict of an executable
// by inode, so a binary rewritten in place is killed at exec with "Code
// Signature Invalid" while a byte-identical copy at a fresh path runs. The
// portable observation is a handle held open across the second write: a
// replaced file leaves the handle on the old, unlinked inode with the old
// bytes, an in-place rewrite shows it the new ones. (The inode NUMBER is not
// the test: ext4 hands a fresh file the number it just freed.)
func TestWriteExecutableReplacesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "prog")
	if err := writeExecutable(p, []byte("one")); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := writeExecutable(p, []byte("two")); err != nil {
		t.Fatal(err)
	}
	old, err := io.ReadAll(held)
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != "one" {
		t.Fatalf("the open handle reads %q after the second write; the executable was rewritten in place, not replaced", old)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Fatalf("mode %v is not executable", fi.Mode())
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "two" {
		t.Fatalf("content %q, want the second write", got)
	}
}
