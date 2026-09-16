package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A second `-o` to the same path must give the binary a new inode. macOS
// caches the code-signature verdict of an executable by inode, so a binary
// rewritten through its existing inode is killed at exec with "Code Signature
// Invalid" while a byte-identical copy at a fresh path runs. The test is the
// inode, which every unix host can check, rather than the exec, which only a
// Mac can.
func TestWriteExecutableReplacesInode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "prog")
	if err := writeExecutable(p, []byte("one")); err != nil {
		t.Fatal(err)
	}
	first := inodeOf(t, p)
	if err := writeExecutable(p, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if again := inodeOf(t, p); again == first {
		t.Fatalf("second write kept inode %d; the executable must be replaced, not overwritten in place", again)
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

func inodeOf(t *testing.T, p string) uint64 {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no inode on this host")
	}
	return uint64(st.Ino)
}
