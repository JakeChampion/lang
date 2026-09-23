package main

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
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

// A path that is not a regular file is written through, never replaced:
// `-o /dev/null` used to delete the null device and leave the ELF in its
// place (#10034). A FIFO stands in for the device, and its mode must survive
// too — a chmod to 0755 on a device is as destructive as the unlink.
func TestWriteExecutableKeepsAFIFO(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(p, 0o622); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	got := make(chan []byte, 1)
	go func() {
		f, err := os.Open(p)
		if err != nil {
			got <- nil
			return
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		got <- b
	}()
	if err := writeExecutable(p, []byte("elf")); err != nil {
		t.Fatal(err)
	}
	// A reader already blocked opening the FIFO waits forever once the path
	// is replaced, so a regression is a timeout rather than a hang.
	select {
	case b := <-got:
		if string(b) != "elf" {
			t.Fatalf("the reader got %q, want the bytes written through the FIFO", b)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the FIFO's reader never saw the write; the path was replaced")
	}
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("the path is now %v, want the FIFO kept", fi.Mode())
	}
	if perm := fi.Mode().Perm(); perm&0o111 != 0 {
		t.Fatalf("the FIFO's mode became %v; a non-regular path must not be chmodded", perm)
	}
}

// A symlink to an executable stays a link: the target is written through and
// stays executable.
func TestWriteExecutableKeepsASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "prog")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeExecutable(link, []byte("new")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link is now %v, want it kept", fi.Mode())
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "new" {
		t.Fatalf("the target reads %q, want the bytes written through the link", b)
	}
	tfi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if tfi.Mode()&0o111 == 0 {
		t.Fatalf("the target's mode %v is not executable", tfi.Mode())
	}
}
