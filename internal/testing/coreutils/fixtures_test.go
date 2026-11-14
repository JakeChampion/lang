package coreutils

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func seedWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func seedMkdir(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
}

func seedSymlink(t *testing.T, dir, target, name string) {
	t.Helper()
	if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
		t.Fatalf("symlink %s: %v", name, err)
	}
}

func seedHardlink(t *testing.T, dir, old, name string) {
	t.Helper()
	if err := os.Link(filepath.Join(dir, old), filepath.Join(dir, name)); err != nil {
		t.Fatalf("link %s: %v", name, err)
	}
}

func seedTouch(t *testing.T, dir, name string, sec int64, nsec int64) {
	t.Helper()
	when := time.Unix(sec, nsec)
	if err := os.Chtimes(filepath.Join(dir, name), when, when); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
}

func seedFifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatalf("mkfifo %s: %v", path, err)
	}
}

// seedSocket makes a Unix socket at path. Darwin lets only root mknod a
// socket, and a socket path has a short length limit, so the socket is bound
// in a short directory and then moved to path.
func seedSocket(t *testing.T, path string) {
	t.Helper()
	shortDir, err := os.MkdirTemp("", "s")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(shortDir) })
	ln, err := net.Listen("unix", filepath.Join(shortDir, "s"))
	if err != nil {
		t.Fatalf("unix socket: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	if err := os.Rename(filepath.Join(shortDir, "s"), path); err != nil {
		t.Fatalf("move the socket to %s: %v", path, err)
	}
}
