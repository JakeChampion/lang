package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestReaderText(t *testing.T) {
	f := e2eharness.MakeReaderTextFixture()
	src := filepath.Join(t.TempDir(), "reader.fern")
	if err := os.WriteFile(src, []byte(f.Source), 0o644); err != nil {
		t.Fatal(err)
	}
	fern := buildLangBinForInterp(t)
	for _, target := range []string{"interp", "arm64-darwin", "arm64-linux", "x86-64-linux", "wasm32-wasi", "component"} {
		t.Run(target, func(t *testing.T) {
			if target == "interp" {
				f.Check(t, exec.Command(fern, "-interp", src))
				return
			}
			var runner []string
			actual := target
			switch target {
			case "arm64-darwin":
				if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
					t.Skip("requires Apple Silicon")
				}
			case "arm64-linux":
				_, qemu := arm64Tooling(t)
				if qemu != "" {
					runner = []string{qemu}
				}
			case "x86-64-linux":
				_, runner = x86_64Tooling(t)
			case "wasm32-wasi", "component":
				if _, err := exec.LookPath("wasmtime"); err != nil {
					t.Skip("requires wasmtime")
				}
				actual, runner = "wasm32-wasi", []string{"wasmtime", "run"}
			}
			bin := filepath.Join(t.TempDir(), "reader")
			args := []string{"-target", actual, "-o", bin}
			if target == "wasm32-wasi" {
				args = append(args, "-emit", "command-module")
			}
			args = append(args, src)
			if out, err := exec.Command(fern, args...).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			argv := append(append([]string{}, runner...), bin)
			f.Check(t, exec.Command(argv[0], argv[1:]...))
		})
	}
}
