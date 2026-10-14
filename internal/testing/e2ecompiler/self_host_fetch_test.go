package e2ecompiler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// tarEntry is one member of a test archive; typeflag 0 means a regular file.
type tarEntry struct {
	name, body string
	typeflag   byte
	pax        map[string]string
}

// tarGz builds a gzip'd tar of entries and returns it with its sha256 hash.
func tarGz(t *testing.T, entries []tarEntry) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg, PAXRecords: e.pax}
		switch e.typeflag {
		case tar.TypeDir:
			h.Typeflag, h.Size, h.Mode = tar.TypeDir, 0, 0o755
		case tar.TypeXGlobalHeader:
			h = &tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: e.pax}
		case tar.TypeSymlink:
			h.Typeflag, h.Size, h.Linkname = tar.TypeSymlink, 0, e.body
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), sha256Ref(buf.Bytes())
}

// sha256Ref is b's hash as a manifest writes it.
func sha256Ref(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// serve answers every request with body.
func serve(t *testing.T, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// fetchEnv is a project directory and a private package store.
type fetchEnv struct {
	dir, cache string
	t          *testing.T
}

func newFetchEnv(t *testing.T, files map[string]string) fetchEnv {
	t.Helper()
	e := fetchEnv{dir: t.TempDir(), cache: t.TempDir(), t: t}
	for name, src := range files {
		p := filepath.Join(e.dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// run runs the shipped fern with args in the project directory, a `-run`
// for the host target.
func (e fetchEnv) run(args ...string) (string, int) {
	e.t.Helper()
	if args[0] == "-run" {
		args = append([]string{"-run", "-target", e2eharness.HostSelfHostTarget()}, args[1:]...)
	}
	cmd := exec.Command(e2eharness.ShippedFern(e.t), args...)
	cmd.Dir = e.dir
	cmd.Env = append(os.Environ(), "FERN_CACHE_DIR="+e.cache)
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

func helperArchive(t *testing.T, extra ...tarEntry) ([]byte, string) {
	return tarGz(t, append(extra,
		tarEntry{name: "helper/fern.toml", body: "[package]\nname = \"helper\"\n"},
		tarEntry{name: "helper/lib.fern", body: "pub function nine(): i32 { return 9; }\n"},
	))
}

const helperMain = "import \"helper\";\nfunction main(): i32 { return helper.nine(); }\n"

// TestShippedFernFetch: a url dependency is refused until `-fetch` puts it in
// the store, after which the program builds from there; a second fetch has
// nothing to do.
func TestShippedFernFetch(t *testing.T) {
	archive, hash := helperArchive(t, tarEntry{typeflag: tar.TypeXGlobalHeader, pax: map[string]string{"comment": "0123abcd"}})
	url := serve(t, archive) + "/helper.tar.gz"
	e := newFetchEnv(t, map[string]string{
		"fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { url = \"" + url + "\", hash = \"" + hash + "\" }\n",
		"main.fern": helperMain,
	})
	if out, code := e.run("-run", "main.fern"); code == 0 || !strings.Contains(out, "is not in the package store — run `fern -fetch") {
		t.Fatalf("before fetching: exit %d\n%s", code, out)
	}
	if out, code := e.run("-fetch"); code != 0 || out != "fetching helper ("+url+")\nfetch: 1 downloaded, 2 package(s) checked\n" {
		t.Fatalf("-fetch: exit %d\n%s", code, out)
	}
	store := filepath.Join(e.cache, "pkgs", strings.TrimPrefix(hash, "sha256:"))
	if _, err := os.Stat(filepath.Join(store, "lib.fern")); err != nil {
		t.Fatalf("the archive's one top-level directory was not unpacked as the package: %v", err)
	}
	if out, code := e.run("-run", "main.fern"); code != 9 {
		t.Fatalf("after fetching: exit %d\n%s", code, out)
	}
	if out, code := e.run("-fetch", e.dir); code != 0 || out != "fetch: 0 downloaded, 2 package(s) checked\n" {
		t.Errorf("second -fetch: exit %d\n%s", code, out)
	}
}

// TestShippedFernFetchRefusals: an archive whose hash is not the manifest's,
// one with an entry leaving the package directory, one with a link, one that
// is not gzip and one cut short are all refused, saying why, and nothing is
// left in the store.
func TestShippedFernFetchRefusals(t *testing.T) {
	good, _ := helperArchive(t)
	_, other := helperArchive(t, tarEntry{name: "helper/extra.fern", body: "\n"})
	escaping, escHash := helperArchive(t, tarEntry{name: "../evil.fern", body: "x"})
	linked, linkHash := helperArchive(t, tarEntry{name: "helper/link", body: "/etc/passwd", typeflag: tar.TypeSymlink})
	plain := []byte("not an archive\n")
	cut := good[:len(good)/2]
	for _, c := range []struct {
		name, hash, want string
		body             []byte
	}{
		{"hash-mismatch", other, "hash mismatch — manifest declares " + other, good},
		{"escaping-entry", escHash, `archive entry "../evil.fern" escapes the package directory`, escaping},
		{"symlink", linkHash, `archive entry "helper/link" has unsupported type 50 (only files and directories)`, linked},
		{"not-gzip", sha256Ref(plain), "cannot gunzip the archive: unsupported compressed data: not gzip", plain},
		{"truncated", sha256Ref(cut), "cannot gunzip the archive: the compressed data ends early", cut},
	} {
		t.Run(c.name, func(t *testing.T) {
			url := serve(t, c.body)
			e := newFetchEnv(t, map[string]string{
				"fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { url = \"" + url + "\", hash = \"" + c.hash + "\" }\n",
			})
			if out, code := e.run("-fetch"); code != 1 || !strings.Contains(out, c.want) {
				t.Errorf("exit %d\n%s", code, out)
			}
			if entries, _ := os.ReadDir(filepath.Join(e.cache, "pkgs")); len(entries) != 0 {
				t.Errorf("the store holds %d entries after a refusal", len(entries))
			}
		})
	}
}

// TestShippedFernFetchFlatArchive: an archive with files at its top level is
// the package as it stands.
func TestShippedFernFetchFlatArchive(t *testing.T) {
	archive, hash := tarGz(t, []tarEntry{
		{name: "fern.toml", body: "[package]\nname = \"helper\"\n"},
		{name: "lib.fern", body: "pub function nine(): i32 { return 9; }\n"},
		{name: "sub/", typeflag: tar.TypeDir},
		{name: "sub/more.fern", body: "\n"},
	})
	e := newFetchEnv(t, map[string]string{
		"fern.toml": "[package]\nname = \"app\"\n[dependencies]\nhelper = { url = \"" + serve(t, archive) + "\", hash = \"" + hash + "\" }\n",
		"main.fern": helperMain,
	})
	if out, code := e.run("-fetch"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if out, code := e.run("-run", "main.fern"); code != 9 {
		t.Errorf("exit %d\n%s", code, out)
	}
}

// TestShippedFernAddURL: `-add NAME url:…` downloads the archive to record
// its hash, and leaves it in the store, so the next build needs no fetch.
func TestShippedFernAddURL(t *testing.T) {
	archive, hash := helperArchive(t)
	url := serve(t, archive) + "/helper.tar.gz"
	e := newFetchEnv(t, map[string]string{
		"fern.toml": "[package]\nname = \"app\"\n",
		"main.fern": helperMain,
	})
	if out, code := e.run("-add", "helper", "url:"+url); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	got, _ := os.ReadFile(filepath.Join(e.dir, "fern.toml"))
	if want := "helper = { url = \"" + url + "\", hash = \"" + hash + "\" }"; !strings.Contains(string(got), want) {
		t.Errorf("fern.toml lacks %q:\n%s", want, got)
	}
	if out, code := e.run("-run", "main.fern"); code != 9 {
		t.Errorf("exit %d\n%s", code, out)
	}
	if out, code := e.run("-add", "other", "url:ftp://example.com/x.tar.gz"); code != 1 || !strings.Contains(out, "add: url must be http(s)") {
		t.Errorf("a non-http url: exit %d\n%s", code, out)
	}
}

// TestShippedFernResolveURL: a version MVS selects from a url index source is
// fetched and verified during `-resolve` and pinned with its url and hash;
// with the store emptied, `-fetch` downloads it again from the lock.
func TestShippedFernResolveURL(t *testing.T) {
	archive, hash := helperArchive(t)
	url := serve(t, archive) + "/helper-1.0.0.tar.gz"
	e := newFetchEnv(t, map[string]string{
		"fern.toml":  "[package]\nname = \"app\"\nindex = \"index.toml\"\n[dependencies]\nhelper = \"1.0.0\"\n",
		"index.toml": "[helper]\n\"1.0.0\" = { url = \"" + url + "\", hash = \"" + hash + "\" }\n",
		"main.fern":  helperMain,
	})
	if out, code := e.run("-resolve"); code != 0 {
		t.Fatalf("-resolve: exit %d\n%s", code, out)
	}
	lock, _ := os.ReadFile(filepath.Join(e.dir, "fern.lock"))
	if !strings.Contains(string(lock), "url = \""+url+"\"\nhash = \""+hash+"\"") {
		t.Errorf("fern.lock:\n%s", lock)
	}
	if out, code := e.run("-run", "main.fern"); code != 9 {
		t.Fatalf("after -resolve: exit %d\n%s", code, out)
	}
	if err := os.RemoveAll(filepath.Join(e.cache, "pkgs")); err != nil {
		t.Fatal(err)
	}
	if out, code := e.run("-fetch"); code != 0 || !strings.Contains(out, "fetching helper@1.0.0 ("+url+")\n") {
		t.Errorf("-fetch from the lock: exit %d\n%s", code, out)
	}
	if out, code := e.run("-run", "main.fern"); code != 9 {
		t.Errorf("after -fetch: exit %d\n%s", code, out)
	}
}

// TestSelfHostCLIFetchNeedsTheShippedFern: a build of fern.fern alone has no
// HTTP client, and says so instead of failing somewhere less clear.
func TestSelfHostCLIFetchNeedsTheShippedFern(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fern.toml"), []byte("[package]\nname = \"app\"\n[dependencies]\nhelper = { url = \"http://127.0.0.1:1/x.tar.gz\", hash = \"sha256:"+strings.Repeat("0", 64)+"\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-fetch", dir)
	cmd.Env = append(os.Environ(), "FERN_CACHE_DIR="+t.TempDir())
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState.ExitCode() != 1 || !strings.Contains(string(out), "needs the shipped `fern`; this build has no HTTP client") {
		t.Errorf("exit %d\n%s", cmd.ProcessState.ExitCode(), out)
	}
}
