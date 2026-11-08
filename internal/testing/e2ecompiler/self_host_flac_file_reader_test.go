package e2ecompiler

import (
	"bytes"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

func flacSeekFileFixture(t *testing.T) []byte {
	t.Helper()
	source, err := os.ReadFile(langSrcAbs(t, "tests/stdlib/audio_vectors.fern"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`Flac \{ name: "fixed-frames", bytes: "([0-9a-f]+)"`).FindSubmatch(source)
	if match == nil {
		t.Fatal("independent FLAC smoke fixture is missing")
	}
	data, err := hex.DecodeString(string(match[1]))
	if err != nil || len(data) < 2 {
		t.Fatalf("fixture data: %v", err)
	}
	return data
}

func TestSelfHostFlacFileReader(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := langSrcAbs(t, "tests/stdlib/flac_file_reader.fern")
	dir := t.TempDir()
	fixture := flacSeekFileFixture(t)
	valid := filepath.Join(dir, "complete.flac")
	truncated := filepath.Join(dir, "truncated.flac")
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
				{"missing", filepath.Join(dir, "missing.flac"), ""},
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

func TestSelfHostAudioFileRange(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := langSrcAbs(t, "tests/stdlib/audio_file_range.fern")
	dir := t.TempDir()
	path := filepath.Join(dir, "sparse.bin")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteAt([]byte{1, 2, 3}, 8589934592)
	closeErr := file.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var command *exec.Cmd
			if target == "wasm32-wasi" {
				command = exec.Command("wasmtime", "run", "--dir", dir+"::/", cli.wasmComponent(t, source, "FERN_STRICT_IR=1"), filepath.Base(path))
			} else {
				command = runX86_64Bin(cli.runner, cli.x86Binary(t, source, "FERN_STRICT_IR=1", "FERN_SANITIZE=1"), path)
			}
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("64-bit file range: %v\n%s", err, output)
			}
		})
	}
}
