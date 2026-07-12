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

	"github.com/jakechampion/lang/internal/manifest"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, manifest.FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAddPathDependency(t *testing.T) {
	dir := writeManifest(t, "[package]\nname = \"app\"\n# keep me\n[dependencies]\nx = { path = \"../x\" }\n")
	if err := runAdd("helper", "path:../helper", dir); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Deps["helper"].Path != filepath.FromSlash("../helper") {
		t.Errorf("helper not added: %+v", m.Deps)
	}
	// Existing dep + comment survive.
	src, _ := os.ReadFile(filepath.Join(dir, manifest.FileName))
	if !strings.Contains(string(src), "# keep me") || !strings.Contains(string(src), `x = { path = "../x" }`) {
		t.Errorf("existing content not preserved:\n%s", src)
	}
}

func TestAddWorkspaceDependencyCreatesTable(t *testing.T) {
	dir := writeManifest(t, "[package]\nname = \"app\"\n")
	if err := runAdd("lexer", "workspace", dir); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Deps["lexer"].Workspace {
		t.Errorf("workspace dep not added: %+v", m.Deps)
	}
}

func TestAddURLComputesAndRecordsHash(t *testing.T) {
	t.Setenv("FERN_CACHE_DIR", t.TempDir())
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := "pub function f(): i32 { return 1; }"
	tw.WriteHeader(&tar.Header{Name: "lib.fern", Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg})
	tw.Write([]byte(content))
	tw.Close()
	gz.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	wantHash := "sha256:" + hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	dir := writeManifest(t, "[package]\nname = \"app\"\n")
	if err := runAdd("remote", "url:"+srv.URL+"/pkg.tar.gz", dir); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := m.Deps["remote"]
	if d.URL != srv.URL+"/pkg.tar.gz" || d.Hash != wantHash {
		t.Fatalf("url dep recorded wrong: url=%q hash=%q want hash %q", d.URL, d.Hash, wantHash)
	}
}

func TestAddDuplicateRejected(t *testing.T) {
	dir := writeManifest(t, "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\" }\n")
	if err := runAdd("helper", "path:../other", dir); err == nil {
		t.Fatal("adding an existing dep should error")
	}
}

func TestAddInvalidNameRejected(t *testing.T) {
	dir := writeManifest(t, "[package]\nname = \"app\"\n")
	if err := runAdd("1bad", "path:../x", dir); err == nil {
		t.Fatal("invalid dep name should error")
	}
	if err := runAdd("bad name", "workspace", dir); err == nil {
		t.Fatal("dep name with a space should error")
	}
}

func TestAddBadSpecRejected(t *testing.T) {
	dir := writeManifest(t, "[package]\nname = \"app\"\n")
	for _, spec := range []string{"1.2", "git:foo", "url:ftp://x", "path:"} {
		if err := runAdd("dep", spec, dir); err == nil {
			t.Errorf("spec %q should be rejected", spec)
		}
	}
	// A rejected add leaves the manifest untouched.
	src, _ := os.ReadFile(filepath.Join(dir, manifest.FileName))
	if strings.Contains(string(src), "dep") {
		t.Errorf("rejected add mutated the manifest:\n%s", src)
	}
}

func TestAddNoManifestErrors(t *testing.T) {
	if err := runAdd("x", "workspace", t.TempDir()); err == nil {
		t.Fatal("add without a fern.toml should error")
	}
}
