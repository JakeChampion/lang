package launcher

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStdlibRootWritesTheEmbeddedTreeOnce(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir, err := StdlibRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"std/io.fern", "core/int.fern", "std/option.fern.md", "std/result.fern.md", "std/csv.fern.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s missing from %s: %v", f, dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "marker"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := StdlibRoot()
	if err != nil {
		t.Fatal(err)
	}
	if again != dir {
		t.Errorf("second call gave %s, want %s", again, dir)
	}
	if _, err := os.Stat(filepath.Join(again, "marker")); err != nil {
		t.Errorf("second call rewrote the directory: %v", err)
	}
}

func TestCompilerPrefersTheEnvironment(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fern-selfhost")
	if err := os.WriteFile(p, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvCompiler, p)
	got, err := Compiler()
	if err != nil || got != p {
		t.Fatalf("Compiler() = %q, %v; want %q", got, err, p)
	}
	t.Setenv(EnvCompiler, filepath.Join(t.TempDir(), "absent"))
	if _, err := Compiler(); err == nil || !strings.Contains(err.Error(), EnvCompiler) {
		t.Errorf("a missing %s gave %v, want an error naming it", EnvCompiler, err)
	}
}

func TestSiblingIsCurrentOnlyWithItsSourcesKey(t *testing.T) {
	key, err := SourcesKey()
	if err != nil {
		t.Skipf("no sources key on this host: %v", err)
	}
	p := filepath.Join(t.TempDir(), "fern-selfhost")
	if current(p) {
		t.Errorf("a missing compiler is current")
	}
	if err := os.WriteFile(p, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if current(p) {
		t.Errorf("a compiler with no key is current")
	}
	for _, stamp := range []string{"0123", key[:len(key)-1] + "x"} {
		if err := os.WriteFile(p+KeySuffix, []byte(stamp+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if current(p) {
			t.Errorf("a compiler keyed %q is current; this fern's key is %q", stamp, key)
		}
	}
	if err := os.WriteFile(p+KeySuffix, []byte(key+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !current(p) {
		t.Errorf("a compiler keyed with this fern's own sources key is not current")
	}
}

func TestDownloadRetriesOnlyTransientFailures(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch {
		case r.URL.Path == "/missing":
			w.WriteHeader(http.StatusNotFound)
		case hits == 1:
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			io.WriteString(w, "ok")
		}
	}))
	defer srv.Close()

	resp, err := download(srv.URL + "/asset")
	if err != nil {
		t.Fatalf("after one 503: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" || hits != 2 {
		t.Errorf("body %q after %d requests, want \"ok\" after 2", body, hits)
	}

	hits = 0
	if _, err := download(srv.URL + "/missing"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("404: err = %v, want the status", err)
	}
	if hits != 1 {
		t.Errorf("404 was requested %d times, want 1", hits)
	}
}

func TestStage0EnvDropsTheCompilerSwitches(t *testing.T) {
	env := []string{"PATH=/usr/bin", "FERN_SANITIZE=1", "HOME=/home/x", "FERN_LEAKCHECK=1", "FERN_RC_CERTIFY_MEMO=/tmp/m", "GOFLAGS=-mod=mod"}
	got := stage0Env(env)
	want := []string{"PATH=/usr/bin", "HOME=/home/x", "GOFLAGS=-mod=mod"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("stage0Env(%q) = %q, want %q", env, got, want)
	}
}
