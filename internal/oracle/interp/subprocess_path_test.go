package interp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// subprocessCommand finds a program as execvp does, so the oracle and the
// native runtimes run the same file.
func TestSubprocessCommandPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fernlocal"), []byte("#!/bin/sh\necho local\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	t.Run("an empty element of PATH is the current directory", func(t *testing.T) {
		t.Setenv("PATH", "/nonexistent::/bin")
		out, err := subprocessCommand("fernlocal", nil).Output()
		if err != nil || strings.TrimSpace(string(out)) != "local" {
			t.Fatalf("got %q, %v; want local", out, err)
		}
	})
	t.Run("no PATH searches /bin and /usr/bin", func(t *testing.T) {
		t.Setenv("PATH", "")
		os.Unsetenv("PATH")
		out, err := subprocessCommand("sh", []string{"-c", "echo fallback"}).Output()
		if err != nil || strings.TrimSpace(string(out)) != "fallback" {
			t.Fatalf("got %q, %v; want fallback", out, err)
		}
	})
}
