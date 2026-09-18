package coreutils

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The package's fixture vocabulary: build a tree for a corpus to run against.
//
// These were written five times over, once per utility that needed them —
// `mv*`, `ln*`, `du*`, `rm*`, `truncate*` — because each author wrote them where
// they happened to be looking. #9310 shared the most complete set rather than
// adding a sixth; #9312 folded the rest in. They live here, beside
// harness_test.go, so that they read as the package's rather than one utility's.
//
// A utility still owns its own fixture FUNCTIONS (mvBasic, lnDir, rmTree, …).
// What is shared is the primitives those build out of.
//
// du keeps its own set deliberately: duWrite takes a byte COUNT rather than
// content, duSparse allocates nothing, and the family is addressed by full path
// because duTree threads one through `j(...)`. Those are different operations,
// not different spellings of these.

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

// seedTouch pins a file's timestamps. Every --update case needs them: the
// two sides run seconds apart, so a fixture that took the wall clock
// would have the source newer than the destination on one run and not on
// the other, and the case would be random rather than a comparison.
func seedTouch(t *testing.T, dir, name string, sec int64, nsec int64) {
	t.Helper()
	when := time.Unix(sec, nsec)
	if err := os.Chtimes(filepath.Join(dir, name), when, when); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
}

// seedFifo takes a full path rather than dir + name: most callers already hold
// one from their own `j(...)` joiner. dd builds its timing pipe at 0o600 and
// keeps its own call — the mode there is the fixture, not an incidental.
func seedFifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatalf("mkfifo %s: %v", path, err)
	}
}
