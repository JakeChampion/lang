package e2ecompiler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The targets here feed hostile bytes to the readers of the package layer
// through drivers/pkg_fuzz_run.fern: the manifest reader, the lock reader,
// the index reader and the archive unpacker. A package, its lock and the
// archive an index names are inputs an attacker controls. Each reader
// refuses foreign bytes with a message, so the one accepted outcome is exit
// 0 with the reader's summary; a signal, a hang or a budget breach fails
// the input. The literate targets share the harness.

// fuzzReader feeds each input as a file to one reader of a driver, and
// accepts exit 0 with a summary that opens with the reader's name.
func fuzzReader(f *testing.F, driverName, reader string, seeds [][]byte) {
	driver := fuzzDriver(f, driverName)
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > fuzzInputLimit {
			t.Skip("over the input limit")
		}
		work := t.TempDir()
		path := filepath.Join(work, "input")
		if err := os.WriteFile(path, input, 0o644); err != nil {
			t.Fatal(err)
		}
		r := runUnderBudget(exec.Command(driver, reader, path))
		if failOutsideBudget(t, driverName+" "+reader, r, input) {
			return
		}
		if r.code != 0 || !strings.HasPrefix(r.stdout, reader+" ") {
			t.Errorf("exit %d on %d bytes, want 0 with a %s summary\nstdout: %q\nstderr: %q", r.code, len(input), reader, r.stdout, r.stderr)
		}
	})
}

// FuzzSelfHostToml feeds hostile bytes to the fern.toml reader and its
// diagnostic formatter.
func FuzzSelfHostToml(f *testing.F) {
	fuzzReader(f, "pkg_fuzz_run", "toml", [][]byte{
		[]byte("[package]\nname = \"app\"\n"),
		[]byte("[package]\nname = \"app\"\nindex = \"reg/index.toml\"\nindex_key = \"ed25519:" + strings.Repeat("a", 64) + "\"\n[dependencies]\nfoo = \"1.1.0\"\nbar = { path = \"../bar\" }\n"),
		[]byte("[workspace]\nmembers = [\"a\", \"b\"]\n[exclude]\nfoo = \"1.0.0\"\n"),
		[]byte("[package]\nname = \"app\"\n[lint]\ncyclomatic-complexity = \"warn\"\n[lint.options]\nmax = 10\n"),
		[]byte("[package]\nname = \"app\"\n[dependencies]\nfoo = \"1.0.0\"\nfoo = \"2.0.0\"\n"),
		[]byte("[package]\nname = \"\\u0000\"\n"),
		[]byte("[package\nname = \"app\n"),
		[]byte("name = \"app\"\n"),
		[]byte("[package]\n" + strings.Repeat("name = \"a\"\n", 3000)),
		[]byte("[package]\nname = \"" + strings.Repeat("a", 60000) + "\"\n"),
		[]byte("\xff\xfe[package]\n"),
		{},
	})
}

// FuzzSelfHostLock feeds hostile bytes to the fern.lock reader.
func FuzzSelfHostLock(f *testing.F) {
	entry := "[[package]]\nname = \"lib\"\nversion = \"1.2.3\"\n"
	fuzzReader(f, "pkg_fuzz_run", "lock", [][]byte{
		[]byte(entry + "path = \"../lib\"\n"),
		[]byte("index_serial = 42\n" + entry + "url = \"https://example.invalid/lib\"\nhash = \"sha256:" + strings.Repeat("a", 64) + "\"\n"),
		[]byte(entry),
		[]byte("[[package]\nname = \"lib\"\n"),
		[]byte(entry + "version = \"9223372036854775808.0.0\"\n"),
		[]byte(strings.Repeat(entry+"path = \"x\"\n", 500)),
		[]byte("index_serial = " + strings.Repeat("9", 400) + "\n"),
		[]byte("\x00\x00[[package]]\n"),
		{},
	})
}

// FuzzSelfHostIndex feeds hostile bytes to the registry index reader.
func FuzzSelfHostIndex(f *testing.F) {
	fuzzReader(f, "pkg_fuzz_run", "index", [][]byte{
		[]byte("[foo]\n\"1.0.0\" = { path = \"foo-1.0.0\" }\n\"1.1.0\" = { path = \"foo-1.1.0\" }\n"),
		[]byte("serial = 7\n[foo]\n\"1.0.0\" = { url = \"https://example.invalid/foo.tar.gz\", hash = \"sha256:" + strings.Repeat("b", 64) + "\" }\n"),
		[]byte("[foo]\n\"1.0.0\" = { path = \"a\", url = \"b\" }\n"),
		[]byte("[foo]\n\"1.0\" = { path = \"a\" }\n"),
		[]byte("[foo]\n\"1.0.0\" = { path = \"a\"\n"),
		[]byte("[foo\n"),
		[]byte(strings.Repeat("[p]\n\"1.0.0\" = { path = \"a\" }\n", 400)),
		[]byte("\xff[foo]\n"),
		{},
	})
}

// fuzzTarGz builds a gzip-compressed tar archive of the named entries.
func fuzzTarGz(f *testing.F, entries map[string]string) []byte {
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			f.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			f.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		f.Fatal(err)
	}
	var out bytes.Buffer
	gw := gzip.NewWriter(&out)
	if _, err := gw.Write(tarBuf.Bytes()); err != nil {
		f.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		f.Fatal(err)
	}
	return out.Bytes()
}

// FuzzSelfHostArchive feeds hostile bytes to the package archive unpacker:
// the gunzip, the tar reader and the entry name rules.
func FuzzSelfHostArchive(f *testing.F) {
	good := fuzzTarGz(f, map[string]string{"lib/fern.toml": "[package]\nname = \"lib\"\n", "lib/lib.fern": "pub function v(): i32 { return 1; }\n"})
	escape := fuzzTarGz(f, map[string]string{"../x": "oops"})
	long := fuzzTarGz(f, map[string]string{strings.Repeat("a", 150) + "/b.fern": "x"})
	truncated := good[:len(good)/2]
	fuzzReader(f, "pkg_fuzz_run", "archive", [][]byte{
		good,
		escape,
		long,
		truncated,
		[]byte("\x1f\x8b\x08\x00junkjunkjunk"),
		bytes.Repeat([]byte{0}, 1024),
		{},
	})
}
