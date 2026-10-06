package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesCoversEveryRoot(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	files, err := Files(root, Sources)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range Sources {
		found := false
		for _, f := range files {
			if strings.HasPrefix(f, r+"/") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no file from root %s", r)
		}
	}
	if !contains(files, "compiler/fern.fern") || !contains(files, "examples/hello.fern") {
		t.Errorf("expected compiler/fern.fern and examples/hello.fern among %d files", len(files))
	}
}

func TestFilesRejectsAnEmptyRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "full"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "full", "a.fern"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := Files(dir, []string{"full"}); err != nil || len(got) != 1 || got[0] != "full/a.fern" {
		t.Fatalf("Files(full) = %v, %v", got, err)
	}
	if _, err := Files(dir, []string{"full", "empty"}); err == nil {
		t.Fatal("an empty root was accepted")
	}
	if _, err := Files(dir, []string{"missing"}); err == nil {
		t.Fatal("a missing root was accepted")
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
