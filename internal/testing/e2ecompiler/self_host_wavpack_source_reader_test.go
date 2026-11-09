package e2ecompiler

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

func wavpackSourceFileFixture(t *testing.T) []byte {
	t.Helper()
	source, err := os.ReadFile(langSrcAbs(t, "tests/stdlib/wavpack_smoke_vectors.fern"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`name: "seek-24-2-35", encoded: "([^"]+)"`).FindSubmatch(source)
	if match == nil {
		t.Fatal("independent WavPack fixture is missing")
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
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSelfHostWavpackSourceReader(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := langSrcAbs(t, "tests/stdlib/wavpack_source_file.fern")
	dir := t.TempDir()
	fixture := wavpackSourceFileFixture(t)
	valid := filepath.Join(dir, "complete.wv")
	truncated := filepath.Join(dir, "truncated.wv")
	for path, data := range map[string][]byte{valid: fixture, truncated: fixture[:len(fixture)-1]} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var bin string
			if target == "wasm32-wasi" {
				bin = cli.wasmComponent(t, source, "FERN_STRICT_IR=1")
			} else {
				bin = cli.x86Binary(t, source, "FERN_STRICT_IR=1", "FERN_SANITIZE=1")
			}
			for _, tc := range []struct{ name, path, message string }{
				{"complete", valid, ""}, {"truncated", truncated, "incomplete audio input"}, {"missing", filepath.Join(dir, "missing.wv"), ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var cmd *exec.Cmd
					if target == "wasm32-wasi" {
						cmd = exec.Command("wasmtime", "run", "--dir", dir+"::/", bin, filepath.Base(tc.path))
					} else {
						cmd = runX86_64Bin(cli.runner, bin, tc.path)
					}
					output, err := cmd.CombinedOutput()
					if tc.name == "complete" {
						if err != nil {
							t.Fatalf("file decode: %v\n%s", err, output)
						}
					} else {
						if err == nil || cmd.ProcessState.ExitCode() != 1 {
							t.Fatalf("invalid input: want exit 1, got %v\n%s", err, output)
						}
						if tc.message != "" && !bytes.Contains(output, []byte(tc.message)) {
							t.Fatalf("lost codec error category: %s", output)
						}
					}
					if target == "x86-64-linux" && !bytes.Contains(output, []byte("live_bytes=0")) {
						t.Fatalf("missing clean sanitizer census: %s", output)
					}
				})
			}
		})
	}
}
