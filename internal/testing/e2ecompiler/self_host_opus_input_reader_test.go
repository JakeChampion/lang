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

func opusInputFileFixture(t *testing.T) []byte {
	t.Helper()
	source, err := os.ReadFile(langSrcAbs(t, "tests/stdlib/opus_file_vectors.fern"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`pub const SMOKE: string = "([A-Za-z0-9+/=]+)";`).FindSubmatch(source)
	if match == nil {
		t.Fatal("independent complete-file smoke fixture is missing")
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
	if err != nil || len(data) < 4 {
		t.Fatalf("fixture data: %v", err)
	}
	size := int(binary.LittleEndian.Uint32(data))
	if size < 1 || size > len(data)-4 {
		t.Fatal("invalid fixture file size")
	}
	return data[4 : 4+size]
}

func TestSelfHostOpusInputReader(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := langSrcAbs(t, "tests/stdlib/opus_input_file.fern")
	dir := t.TempDir()
	fixture := opusInputFileFixture(t)
	valid := filepath.Join(dir, "complete.opus")
	truncated := filepath.Join(dir, "truncated.opus")
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
				{"complete", valid, ""},
				{"truncated", truncated, "incomplete audio input"},
				{"missing", filepath.Join(dir, "missing.opus"), ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var command *exec.Cmd
					if target == "wasm32-wasi" {
						command = exec.Command("wasmtime", "run", "--dir", dir+"::/", bin, filepath.Base(tc.path))
					} else {
						command = runX86_64Bin(cli.runner, bin, tc.path)
					}
					output, err := command.CombinedOutput()
					if tc.name == "complete" {
						if err != nil {
							t.Fatalf("file decode: %v\n%s", err, output)
						}
						return
					}
					if err == nil || command.ProcessState.ExitCode() != 1 {
						t.Fatalf("invalid input: want exit 1, got %v\n%s", err, output)
					}
					if tc.message != "" && !bytes.Contains(output, []byte(tc.message)) {
						t.Fatalf("error lost its codec category:\n%s", output)
					}
				})
			}
		})
	}
}
