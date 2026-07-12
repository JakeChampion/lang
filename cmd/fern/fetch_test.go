package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/modload"
)

// `fern -fetch` end-to-end: an app declares a url+hash dependency, fetch
// downloads + verifies + unpacks it into the store, and a subsequent
// LOAD (which itself never fetches) resolves the import from the store.
func TestRunFetchThenLoad(t *testing.T) {
	t.Setenv("FERN_CACHE_DIR", t.TempDir())

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range map[string]string{
		"helper/fern.toml": "[package]\nname = \"helper\"\n",
		"helper/lib.fern":  "pub function nine(): i32 { return 9; }",
	} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(content))
	}
	tw.Close()
	gz.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	hash := "sha256:" + hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "[package]\nname = \"app\"\n[dependencies]\nhelper = { url = \"" + srv.URL + "/helper.tar.gz\", hash = \"" + hash + "\" }\n"
	if err := os.WriteFile(filepath.Join(app, "fern.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	main := `import "helper";` + "\n" + `function main(): i32 { return helper.nine(); }`
	if err := os.WriteFile(filepath.Join(app, "main.fern"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}

	// Load before fetch: must error pointing at -fetch, not download.
	if _, _, err := modload.Load(filepath.Join(app, "main.fern")); err == nil || !strings.Contains(err.Error(), "-fetch") {
		t.Fatalf("pre-fetch load should point at `fern -fetch`, got %v", err)
	}
	if err := runFetch(app); err != nil {
		t.Fatal(err)
	}
	prog, _, err := modload.Load(filepath.Join(app, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range prog.Funcs {
		if fn.Name == "lib__nine" {
			return
		}
	}
	t.Fatal("fetched dependency not loaded")
}
