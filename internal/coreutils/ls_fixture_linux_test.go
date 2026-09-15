package coreutils

import (
	"io/fs"
	"os"
	"path/filepath"
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
