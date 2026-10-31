package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	lock, err := mvs.ReadLock(app)
	if err != nil {
		t.Fatal(err)
	}
	locked := lock.Packages
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
	lock, err := mvs.ReadLock(app)
	if err != nil {
		t.Fatal(err)
	}
	locked := lock.Packages
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

func TestResolveSignedIndex(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	const index = "[foo]\n\"1.0.0\" = { path = \"pkgs/foo-1.0.0\" }\n"
	signature := hex.EncodeToString(ed25519.Sign(priv, []byte(index))) + "\n"
	keyOf := func(k ed25519.PublicKey) string { return "ed25519:" + hex.EncodeToString(k) }
	manifest := func(key string) string {
		return "[package]\nname = \"app\"\nindex = \"index.toml\"\nindex_key = \"" + key + "\"\n[dependencies]\nfoo = \"1.0.0\"\n"
	}
	for _, c := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"signed", map[string]string{"app/fern.toml": manifest(keyOf(pub)), "app/index.toml": index, "app/index.toml.sig": signature}, ""},
		{"no signature file", map[string]string{"app/fern.toml": manifest(keyOf(pub)), "app/index.toml": index}, "fern.toml names index_key but <ROOT>/app/index.toml.sig cannot be read"},
		{"altered index", map[string]string{"app/fern.toml": manifest(keyOf(pub)), "app/index.toml": index + "\"1.1.0\" = { path = \"pkgs/foo-1.1.0\" }\n", "app/index.toml.sig": signature}, "the signature in <ROOT>/app/index.toml.sig does not verify against index_key"},
		{"wrong key", map[string]string{"app/fern.toml": manifest(keyOf(otherPub)), "app/index.toml": index, "app/index.toml.sig": signature}, "does not verify against index_key"},
		{"malformed signature", map[string]string{"app/fern.toml": manifest(keyOf(pub)), "app/index.toml": index, "app/index.toml.sig": "not hex\n"}, "index <ROOT>/app/index.toml.sig: must be 128 hex digits of the ed25519 signature over the index"},
		{"unsigned without a key", map[string]string{"app/fern.toml": "[package]\nname = \"app\"\nindex = \"index.toml\"\n[dependencies]\nfoo = \"1.0.0\"\n", "app/index.toml": index}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"app/pkgs/foo-1.0.0/fern.toml": "[package]\nname = \"foo\"\n", "app/pkgs/foo-1.1.0/fern.toml": "[package]\nname = \"foo\"\n"}
			for k, v := range c.files {
				files[k] = v
			}
			root := writeResolveTree(t, files)
			err := runResolve(filepath.Join(root, "app"))
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(root, "app", "fern.lock")); err != nil {
					t.Fatalf("no lock written: %v", err)
				}
				return
			}
			want := strings.ReplaceAll(c.want, "<ROOT>", root)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want %q", err, want)
			}
			if _, err := os.Stat(filepath.Join(root, "app", "fern.lock")); err == nil {
				t.Fatal("a refused resolve wrote fern.lock")
			}
		})
	}
}

// fern -resolve records the index serial in fern.lock and refuses a later
// index whose serial is below it or that has lost it.
func TestResolveIndexSerial(t *testing.T) {
	const entries = "[foo]\n\"1.0.0\" = { path = \"pkgs/foo-1.0.0\" }\n"
	lockAt := func(serial string) string {
		return "# generated by `fern -resolve`; do not edit by hand\nindex_serial = " + serial + "\n\n[[package]]\nname = \"foo\"\nversion = \"1.0.0\"\npath = \"pkgs/foo-1.0.0\"\n"
	}
	for _, c := range []struct {
		name, index, lock string
		wantSerial        string
		want              string
	}{
		{"first resolve", "serial = 5\n" + entries, "", "5", ""},
		{"no serial, no record", entries, "", "", ""},
		{"same serial", "serial = 5\n" + entries, lockAt("5"), "5", ""},
		{"newer serial", "serial = 6\n" + entries, lockAt("5"), "6", ""},
		{"more digits", "serial = 10\n" + entries, lockAt("9"), "10", ""},
		{"rolled back", "serial = 4\n" + entries, lockAt("5"), "", "index <ROOT>/app/index.toml: serial 4 is below index serial 5 that <ROOT>/app/fern.lock records"},
		{"rolled back, fewer digits", "serial = 9\n" + entries, lockAt("10"), "", "serial 9 is below index serial 10"},
		{"serial removed", entries, lockAt("5"), "", "index <ROOT>/app/index.toml: has no serial but <ROOT>/app/fern.lock records index serial 5"},
		{"malformed serial", "serial = 05\n" + entries, "", "", "index <ROOT>/app/index.toml: line 1: serial must be 1 to 18 decimal digits with no leading zero, got \"05\""},
		{"serial twice", "serial = 5\nserial = 6\n" + entries, "", "", "line 2: serial given twice"},
		{"malformed lock serial", "serial = 5\n" + entries, "index_serial = x\n", "", "<ROOT>/app/fern.lock line 1: index_serial must be 1 to 18 decimal digits"},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{
				"app/fern.toml":                "[package]\nname = \"app\"\nindex = \"index.toml\"\n[dependencies]\nfoo = \"1.0.0\"\n",
				"app/index.toml":               c.index,
				"app/pkgs/foo-1.0.0/fern.toml": "[package]\nname = \"foo\"\n",
			}
			if c.lock != "" {
				files["app/fern.lock"] = c.lock
			}
			root := writeResolveTree(t, files)
			app := filepath.Join(root, "app")
			err := runResolve(app)
			if c.want != "" {
				want := strings.ReplaceAll(c.want, "<ROOT>", root)
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want %q", err, want)
				}
				after, _ := os.ReadFile(filepath.Join(app, "fern.lock"))
				if string(after) != c.lock {
					t.Fatalf("a refused resolve changed fern.lock:\n%s", after)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			lock, err := mvs.ReadLock(app)
			if err != nil {
				t.Fatal(err)
			}
			if lock.IndexSerial != c.wantSerial {
				t.Fatalf("lock records serial %q, want %q", lock.IndexSerial, c.wantSerial)
			}
		})
	}
}
