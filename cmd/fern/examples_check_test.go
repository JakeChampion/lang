package main

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// Every example, Fern-side test and benchmark checks clean. The Examples lane
// compiles only the top-level `examples/*.fern`, and the printer corpus only
// parses, so a program under a subdirectory could stop type-checking with
// nothing noticing — two probes did. The compiler's sources have gates of
// their own.
func TestEveryExampleChecks(t *testing.T) {
	roots := []string{"examples", "tests", "bench"}
	var files []string
	for _, sub := range roots {
		err := filepath.WalkDir(filepath.Join("..", "..", sub), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".fern") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) < 300 {
		t.Fatalf("found %d programs under %v; the walk is not reading the tree", len(files), roots)
	}
	for _, f := range files {
		t.Run(strings.TrimPrefix(filepath.ToSlash(f), "../../"), func(t *testing.T) {
			t.Parallel()
			if err := runCheck(f, ""); err != nil {
				t.Errorf("%v", err)
			}
		})
	}
}
