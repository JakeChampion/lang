package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestReaderBytesRetainedShortReads(t *testing.T) {
	compiler := buildLangBinForInterp(t)
	for _, target := range []struct {
		name   string
		target string
		emit   string
		qemu   string
	}{
		{"darwin", "arm64-darwin", "", ""},
		{"arm64", "arm64-linux", "", "qemu-aarch64"},
		{"x86_64", "x86-64-linux", "", "qemu-x86_64"},
		{"preview1", "wasm32-wasi", "command-module", ""},
		{"preview2", "wasm32-wasi", "", ""},
	} {
		t.Run(target.name, func(t *testing.T) {
			var runner []string
			switch target.target {
			case "arm64-darwin":
				if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
					t.Skip("requires Apple Silicon")
				}
			case "wasm32-wasi":
				wasmtime, err := exec.LookPath("wasmtime")
				if err != nil {
					t.Skip("wasmtime is not installed")
				}
				runner = []string{wasmtime, "run", "--dir=."}
			default:
				native := runtime.GOOS == "linux" && ((runtime.GOARCH == "arm64" && target.target == "arm64-linux") || (runtime.GOARCH == "amd64" && target.target == "x86-64-linux"))
				if !native {
					qemu, err := exec.LookPath(target.qemu)
					if err != nil {
						t.Skip("target emulator is not installed")
					}
					runner = []string{qemu}
				}
			}
			for _, tc := range []struct {
				name     string
				request  int
				sanitize bool
			}{
				{"full", 1024, false},
				{"short", 65536, false},
				{"short-sanitized", 65536, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					// Bootstrap Reader and IoError values still use immortal
					// headers (rcResultImmortal). Measure their fixed retention
					// with a no-read control; every additional read must balance.
					var retained, retainedBytes int64
					for _, reads := range []int{0, 64} {
						dir := t.TempDir()
						data := make([]byte, 1024)
						for i := range data {
							data[i] = byte(i)
						}
						if err := os.WriteFile(filepath.Join(dir, "input"), data, 0o644); err != nil {
							t.Fatal(err)
						}
						src, bin := filepath.Join(dir, "reader.fern"), filepath.Join(dir, "reader")
						if err := os.WriteFile(src, []byte(e2eharness.ReaderBytesRetainedProgram(tc.request, 1, reads, tc.sanitize)), 0o644); err != nil {
							t.Fatal(err)
						}
						args := []string{"-target", target.target, "-o", bin}
						if target.emit != "" {
							args = append(args, "-emit", target.emit)
						}
						compile := exec.Command(compiler, append(args, src)...)
						for _, entry := range os.Environ() {
							if !strings.HasPrefix(entry, "FERN_SANITIZE=") && !strings.HasPrefix(entry, "FERN_RC_FREE_DEBUG=") {
								compile.Env = append(compile.Env, entry)
							}
						}
						compile.Env = append(compile.Env, "FERN_LEAKCHECK=1")
						if tc.sanitize {
							compile.Env = append(compile.Env, "FERN_SANITIZE=1")
						}
						if out, err := compile.CombinedOutput(); err != nil {
							t.Fatalf("compile: %v\n%s", err, out)
						}
						command := append(append([]string{}, runner...), bin)
						run := exec.Command(command[0], command[1:]...)
						run.Dir = dir
						out, diagnostic, code := runSplit(t, run)
						if code != 0 || (out != "" && out != "0\n") {
							t.Fatalf("retained reads: exit %d\n%s\n%s", code, out, diagnostic)
						}
						line, rest, _ := strings.Cut(diagnostic, "\n")
						allocs, frees, live := parseLeakCheckLine(t, line+"\n")
						if rest != "" && rest != fmt.Sprintf("fern-sanitizer: leak %d bytes in %d blocks\n", live, allocs-frees) {
							t.Fatalf("unexpected runtime diagnostic: %s", diagnostic)
						}
						if reads == 0 {
							retained, retainedBytes = allocs-frees, live
							t.Logf("fixed Reader/IoError retention: %d blocks, %d bytes", retained, retainedBytes)
						} else if allocs-frees != retained || live != retainedBytes {
							t.Fatalf("reads leaked beyond control (%d blocks, %d bytes): %s", retained, retainedBytes, diagnostic)
						}
					}
				})
			}
		})
	}
}
