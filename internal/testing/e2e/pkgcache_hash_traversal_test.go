package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestLockHashOutsideStoreFailsBuild(t *testing.T) {
	base := t.TempDir()
	cache := filepath.Join(base, "cache")
	app := filepath.Join(base, "app")
	outside := filepath.Join(base, "outside", "evil")
	for _, d := range []string{filepath.Join(cache, "pkgs"), app, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(outside, "fern.toml"), "[package]\nname = \"evil\"\nversion = \"1.0.0\"\n")
	write(filepath.Join(outside, "lib.fern"), "pub function hi(): i32 { return 42; }\n")
	write(filepath.Join(app, "fern.toml"), "[package]\nname = \"app\"\nversion = \"0.1.0\"\n\n[dependencies]\nevil = { version = \"1.0.0\" }\n")
	write(filepath.Join(app, "main.fern"), "import \"evil\";\nfunction main(): i32 { return evil.hi(); }\n")

	rel, err := filepath.Rel(filepath.Join(cache, "pkgs"), outside)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(app, "fern.lock"), "[[package]]\nname = \"evil\"\nversion = \"1.0.0\"\n"+
		"url = \"https://example.invalid/x.tar.gz\"\nhash = \"sha256:"+filepath.ToSlash(rel)+"\"\n")

	t.Setenv("FERN_CACHE_DIR", cache)
	_, _, err = modload.Load(filepath.Join(app, "main.fern"))
	if err == nil {
		t.Fatal("a lock hash resolving outside the package store loaded the program")
	}
	if !strings.Contains(err.Error(), "hash") {
		t.Errorf("error should name the offending field, got %v", err)
	}
}
