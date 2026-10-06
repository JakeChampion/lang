package pkgcache

import (
	"path/filepath"
	"strings"
	"testing"
)

// A well-formed hash still resolves, and always to a direct child of the
// store root.
func TestDirValidHashStaysUnderRoot(t *testing.T) {
	t.Setenv("FERN_CACHE_DIR", t.TempDir())
	hexpart := strings.Repeat("ab", 32)
	dir, present, err := Dir("sha256:" + hexpart)
	if err != nil {
		t.Fatal(err)
	}
	if present {
		t.Error("an empty store should report the package absent")
	}
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(root, hexpart) {
		t.Errorf("dir = %q, want %q", dir, filepath.Join(root, hexpart))
	}
}

func TestFetchRejectsMalformedHash(t *testing.T) {
	t.Setenv("FERN_CACHE_DIR", t.TempDir())
	if _, err := Fetch("https://example.invalid/x.tar.gz", "sha256:../../outside"); err == nil {
		t.Error("Fetch should reject a traversing hash before contacting the network")
	}
}
