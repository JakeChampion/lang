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
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/pkg/mvs"
)

func writeResolveTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestResolveMVSAndLoad(t *testing.T) {
	root := writeResolveTree(t, map[string]string{
		"app/fern.toml":               "[package]\nname = \"app\"\nindex = \"reg/index.toml\"\n[dependencies]\nfoo = \"1.1.0\"\nbar = \"1.0.0\"\n",
		"app/main.fern":               `import "foo";` + "\n" + `import "bar";` + "\n" + `function main(): i32 { return foo.v() + bar.v(); }`,
		"app/reg/index.toml":          "[foo]\n\"1.0.0\" = { path = \"foo-1.0.0\" }\n\"1.1.0\" = { path = \"foo-1.1.0\" }\n[bar]\n\"1.0.0\" = { path = \"bar-1.0.0\" }\n\"2.0.0\" = { path = \"bar-2.0.0\" }\n",
		"app/reg/foo-1.0.0/fern.toml": "[package]\nname = \"foo\"\n",
		"app/reg/foo-1.0.0/lib.fern":  "pub function v(): i32 { return 10; }",
		"app/reg/foo-1.1.0/fern.toml": "[package]\nname = \"foo\"\nindex = \"../index.toml\"\n[dependencies]\nbar = \"2.0.0\"\n",
		"app/reg/foo-1.1.0/lib.fern":  "pub function v(): i32 { return 11; }",
		"app/reg/bar-1.0.0/fern.toml": "[package]\nname = \"bar\"\n",
		"app/reg/bar-1.0.0/lib.fern":  "pub function v(): i32 { return 100; }",
		"app/reg/bar-2.0.0/fern.toml": "[package]\nname = \"bar\"\n",
		"app/reg/bar-2.0.0/lib.fern":  "pub function v(): i32 { return 200; }",
	})
	app := filepath.Join(root, "app")

	if _, _, err := modload.Load(filepath.Join(app, "main.fern")); err == nil {
		t.Fatal("load before resolve should error (no fern.lock)")
	}
	if err := runResolve(app); err != nil {
		t.Fatal(err)
	}
	locked, err := mvs.ReadLock(app)
	if err != nil {
		t.Fatal(err)
	}
	if locked["bar"].Version.String() != "2.0.0" {
		t.Fatalf("bar should be uplifted to 2.0.0 by foo's requirement, got %s", locked["bar"].Version)
	}
	if locked["foo"].Version.String() != "1.1.0" {
		t.Fatalf("foo = %s, want 1.1.0", locked["foo"].Version)
	}
	prog, _, err := modload.Load(filepath.Join(app, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, fn := range prog.Funcs {
		found[fn.Name] = true
	}
	if !found["lib__v"] {
		t.Fatalf("versioned dep lib modules not loaded: %v", found)
	}
}

func TestResolveURLIndex(t *testing.T) {
	t.Setenv("FERN_CACHE_DIR", t.TempDir())
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range map[string]string{
		"fern.toml": "[package]\nname = \"foo\"\n",
		"lib.fern":  "pub function v(): i32 { return 7; }",
	} {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg})
		tw.Write([]byte(content))
	}
	tw.Close()
	gz.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	hash := "sha256:" + hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(archive) }))
	defer srv.Close()

	root := writeResolveTree(t, map[string]string{
		"app/fern.toml":  "[package]\nname = \"app\"\nindex = \"index.toml\"\n[dependencies]\nfoo = \"1.0.0\"\n",
		"app/main.fern":  `import "foo";` + "\n" + `function main(): i32 { return foo.v(); }`,
		"app/index.toml": "[foo]\n\"1.0.0\" = { url = \"" + srv.URL + "/foo.tar.gz\", hash = \"" + hash + "\" }\n",
	})
	app := filepath.Join(root, "app")
	if err := runResolve(app); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	if _, _, err := modload.Load(filepath.Join(app, "main.fern")); err != nil {
		t.Fatalf("url-versioned dep should resolve from the store: %v", err)
	}
}

func TestResolveExcludeTopLevelOnly(t *testing.T) {
	root := writeResolveTree(t, map[string]string{
		"app/fern.toml":               "[package]\nname = \"app\"\nindex = \"reg/index.toml\"\n[dependencies]\nfoo = \"1.1.0\"\nbar = \"1.0.0\"\n[exclude]\nbar = [\"2.0.0\"]\n",
		"app/reg/index.toml":          "[foo]\n\"1.1.0\" = { path = \"foo-1.1.0\" }\n[bar]\n\"1.0.0\" = { path = \"bar-1.0.0\" }\n\"2.0.0\" = { path = \"bar-2.0.0\" }\n\"2.1.0\" = { path = \"bar-2.1.0\" }\n",
		"app/reg/foo-1.1.0/fern.toml": "[package]\nname = \"foo\"\nindex = \"../index.toml\"\n[dependencies]\nbar = \"2.0.0\"\n[exclude]\nbar = [\"2.1.0\"]\n",
		"app/reg/bar-1.0.0/fern.toml": "[package]\nname = \"bar\"\n",
		"app/reg/bar-2.0.0/fern.toml": "[package]\nname = \"bar\"\n",
		"app/reg/bar-2.1.0/fern.toml": "[package]\nname = \"bar\"\n",
	})
	app := filepath.Join(root, "app")
	if err := runResolve(app); err != nil {
		t.Fatal(err)
	}
	locked, err := mvs.ReadLock(app)
	if err != nil {
		t.Fatal(err)
	}
	if locked["bar"].Version.String() != "2.1.0" {
		t.Fatalf("bar = %s, want 2.1.0 (2.0.0 excluded by root; foo's own exclude of 2.1.0 ignored)", locked["bar"].Version)
	}
}

func TestResolveNoIndexErrors(t *testing.T) {
	root := writeResolveTree(t, map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nfoo = \"1.0.0\"\n",
	})
	if err := runResolve(filepath.Join(root, "app")); err == nil {
		t.Fatal("versioned deps without an index should error")
	}
}
