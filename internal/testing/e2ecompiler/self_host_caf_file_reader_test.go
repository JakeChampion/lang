package e2ecompiler

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

func cafSeekFileFixture(t *testing.T) []byte {
	t.Helper()
	text, err := os.ReadFile(langSrcAbs(t, "tests/stdlib/caf_smoke_vectors.fern"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`encoded: "([A-Za-z0-9+/=]+)"`).FindSubmatch(text)
	if match == nil {
		t.Fatal("independent CAF fixture is missing")
	}
	packed, err := base64.StdEncoding.DecodeString(string(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zlib.NewReader(bytes.NewReader(packed))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil || len(data) < 52 || string(data[:4]) != "caff" {
		t.Fatalf("CAF fixture: %v", err)
	}
	return data
}

func TestSelfHostCafFileReader(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := langSrcAbs(t, "tests/stdlib/caf_file_reader.fern")
	dir := t.TempDir()
	fixture := cafSeekFileFixture(t)
	for name, data := range map[string][]byte{"complete.caf": fixture, "truncated.caf": fixture[:len(fixture)-1]} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	end := 20 + int(binary.BigEndian.Uint64(fixture[12:20]))
	if end < 20 || end > len(fixture) {
		t.Fatal("CAF description extent")
	}
	sparse, err := os.OpenFile(filepath.Join(dir, "sparse.caf"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 12)
	copy(header, "free")
	binary.BigEndian.PutUint64(header[4:], 1<<32)
	_, prefixErr := sparse.Write(append(append([]byte{}, fixture[:end]...), header...))
	_, suffixErr := sparse.WriteAt(fixture[end:], int64(end+12)+(1<<32))
	closeErr := sparse.Close()
	for _, err := range []error{prefixErr, suffixErr, closeErr} {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var bin string
			if target == "wasm32-wasi" {
				bin = cli.wasmComponent(t, source, "FERN_STRICT_IR=1")
			} else {
				bin = cli.x86Binary(t, source, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			}
			for _, name := range []string{"complete.caf", "sparse.caf", "truncated.caf", "missing.caf"} {
				t.Run(name, func(t *testing.T) {
					var command *exec.Cmd
					if target == "wasm32-wasi" {
						command = exec.Command("wasmtime", "run", "--dir", dir+"::/", bin, name)
					} else {
						command = runX86_64Bin(cli.runner, bin, filepath.Join(dir, name))
					}
					output, err := command.CombinedOutput()
					if name == "complete.caf" || name == "sparse.caf" {
						if err != nil {
							t.Fatalf("Reader decode: %v\n%s", err, output)
						}
						if target == "x86-64-linux" {
							assertBalancedCensus(t, string(output))
						}
						return
					}
					if err == nil || command.ProcessState.ExitCode() != 1 {
						t.Fatalf("invalid file: want exit 1, got %v\n%s", err, output)
					}
					if name == "truncated.caf" && !bytes.Contains(output, []byte("incomplete audio input")) {
						t.Fatalf("codec error category:\n%s", output)
					}
				})
			}
		})
	}
}
