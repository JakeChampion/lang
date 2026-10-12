//go:build linux

package coreutils

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Recursive listing cases share this tree with cases that print and sort by
// access time. Reading a directory must not change their input between sides.
func TestLsTreeTimesSurviveReads(t *testing.T) {
	root := lsTree(t)
	before := map[string]syscall.Stat_t{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			var st syscall.Stat_t
			if err := syscall.Lstat(path, &st); err != nil {
				return err
			}
			// WalkDir visits the directory before reading its children.
			before[path] = st
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if _, err := os.Readlink(path); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range before {
		var got syscall.Stat_t
		if err := syscall.Lstat(path, &got); err != nil {
			t.Fatal(err)
		}
		if got.Atim != want.Atim || got.Mtim != want.Mtim || got.Ctim != want.Ctim {
			t.Errorf("reading %s changed timestamps: atime %v -> %v, mtime %v -> %v, ctime %v -> %v",
				path, want.Atim, got.Atim, want.Mtim, got.Mtim, want.Ctim, got.Ctim)
		}
	}
}

// lsDeepTree is a chain of directories whose relative path runs past
// PATH_MAX, built through descriptors because no path reaches its bottom.
// A regular file and a symlink sit at the top level for the indicator
// cases, which stat the one and not the other.
func lsDeepTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	name := strings.Repeat("d", 100)
	fd, err := syscall.Open(root, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for level := 0; level < 45; level++ {
		if err := syscall.Mkdirat(fd, name, 0o755); err != nil {
			t.Fatal(err)
		}
		next, err := syscall.Openat(fd, name, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
		syscall.Close(fd)
		if err != nil {
			t.Fatal(err)
		}
		fd = next
	}
	syscall.Close(fd)
	return root
}

// GNU does not stat a directory to recurse into it, so the one past
// PATH_MAX is a directory it cannot open — named once on standard error,
// with no header — rather than an entry it cannot access.
func TestLsRecursivePastPathMax(t *testing.T) {
	dir := lsDeepTree(t)
	var cases []invocation
	for _, flags := range [][]string{{"-R"}, {"-lR"}, {"-FR"}, {"-aR"}, {"-LR"}, {"-iR"}, {"-R", "--group-directories-first"}} {
		cases = append(cases, invocation{name: strings.Join(flags, ""), args: append(flags, "."), dir: dir})
	}
	requireParity(t, "ls", cases)
}
