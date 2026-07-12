package pkgcache

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
)

// tarGz builds an in-memory .tar.gz of name→content entries (dirs are
// created implicitly; entries under a shared top-level dir exercise the
// root-stripping convention).
func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestFetchVerifiesUnpacksAndCaches(t *testing.T) {
	t.Setenv("FERN_CACHE_DIR", t.TempDir())
	archive := tarGz(t, map[string]string{
		"helper/fern.toml": "[package]\nname = \"helper\"\n",
		"helper/lib.fern":  "pub function three(): i32 { return 3; }",
	})
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(archive)
	}))
	defer srv.Close()

	dir, err := Fetch(srv.URL+"/helper.tar.gz", hashOf(archive))
	if err != nil {
		t.Fatal(err)
	}
	// Single top-level dir is stripped: lib.fern sits at the package root.
	b, err := os.ReadFile(filepath.Join(dir, "lib.fern"))
	if err != nil || !strings.Contains(string(b), "three") {
		t.Fatalf("unpacked tree wrong: %v / %q", err, b)
	}
	// Content-addressed: the second fetch never contacts the server.
	srv.Close()
	if _, err := Fetch(srv.URL+"/helper.tar.gz", hashOf(archive)); err != nil {
		t.Fatalf("cache hit should not need the server: %v", err)
	}
	if hits != 1 {
		t.Errorf("server hit %d times, want 1", hits)
	}
}

func TestFetchRejectsHashMismatch(t *testing.T) {
	t.Setenv("FERN_CACHE_DIR", t.TempDir())
	archive := tarGz(t, map[string]string{"lib.fern": "pub function f(): i32 { return 1; }"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not the archive at all"))
	}))
	defer srv.Close()
	declared := hashOf(archive)
	_, err := Fetch(srv.URL+"/pkg.tar.gz", declared)
	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("want hash-mismatch rejection, got %v", err)
	}
	// Nothing may have entered the store.
	if _, present, _ := Dir(declared); present {
		t.Error("mismatching archive must not be unpacked into the store")
	}
}

func TestFetchRejectsEscapingEntries(t *testing.T) {
	t.Setenv("FERN_CACHE_DIR", t.TempDir())
	archive := tarGz(t, map[string]string{"../evil.fern": "x"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()
	if _, err := Fetch(srv.URL+"/pkg.tar.gz", hashOf(archive)); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("want path-escape rejection, got %v", err)
	}
}

func TestFetchFlatArchiveKeepsRoot(t *testing.T) {
	t.Setenv("FERN_CACHE_DIR", t.TempDir())
	archive := tarGz(t, map[string]string{
		"fern.toml": "[package]\nname = \"h\"\n",
		"lib.fern":  "pub function f(): i32 { return 1; }",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()
	dir, err := Fetch(srv.URL+"/pkg.tar.gz", hashOf(archive))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "lib.fern")); err != nil {
		t.Fatalf("flat archive should unpack at the root: %v", err)
	}
}

func TestDirBadHashScheme(t *testing.T) {
	if _, _, err := Dir("blake3:abc"); err == nil {
		t.Error("non-sha256 hash should error")
	}
}
