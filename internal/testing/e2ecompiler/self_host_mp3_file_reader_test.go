package e2ecompiler

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

func mp3SeekFileFixture(t *testing.T) []byte {
	t.Helper()
	source, err := os.ReadFile(langSrcAbs(t, "tests/stdlib/mp3_vectors.fern"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`Vector \{ name: "gapless-vbr-16000", bytes: "([0-9a-f]+)"`).FindSubmatch(source)
	if match == nil {
		t.Fatal("independent MP3 smoke fixture is missing")
	}
	data, err := hex.DecodeString(string(match[1]))
	if err != nil || len(data) < 2 {
		t.Fatalf("fixture data: %v", err)
	}
	return data
}

func mp3FileWithTags(fixture []byte) []byte {
	out := []byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, 3, 'a', 'b', 'c'}
	out = append(out, fixture...)
	payload := append([]byte{}, fixture...)
	payload = append(payload, make([]byte, 65537)...)
	out = append(out, payload...)
	footer := make([]byte, 32)
	copy(footer, "APETAGEX")
	binary.LittleEndian.PutUint32(footer[8:], 2000)
	binary.LittleEndian.PutUint32(footer[12:], uint32(len(payload)+32))
	out = append(out, footer...)
	tag := make([]byte, 128)
	copy(tag, "TAG")
	return append(out, tag...)
}

func TestSelfHostMp3FileReader(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := langSrcAbs(t, "tests/stdlib/mp3_file_reader.fern")
	dir := t.TempDir()
	fixture := mp3SeekFileFixture(t)
	valid := filepath.Join(dir, "complete.mp3")
	truncated := filepath.Join(dir, "truncated.mp3")
	tagged := filepath.Join(dir, "tagged.mp3")
	for path, data := range map[string][]byte{valid: fixture, truncated: fixture[:len(fixture)-1], tagged: mp3FileWithTags(fixture)} {
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
				{"tagged", tagged, ""},
				{"truncated", truncated, "incomplete audio input"},
				{"missing", filepath.Join(dir, "missing.mp3"), ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var command *exec.Cmd
					if target == "wasm32-wasi" {
						command = exec.Command("wasmtime", "run", "--dir", dir+"::/", bin, filepath.Base(tc.path))
					} else {
						command = runX86_64Bin(cli.runner, bin, tc.path)
					}
					output, err := command.CombinedOutput()
					if tc.name == "complete" || tc.name == "tagged" {
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
