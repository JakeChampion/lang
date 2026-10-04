package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestReadLineText(t *testing.T) {
	src := filepath.Join(t.TempDir(), "lines.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ReadLineTextProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	fern := buildLangBinForInterp(t)
	for _, target := range []string{"interp", "arm64-darwin", "arm64-linux", "x86-64-linux", "wasm32-wasi", "component"} {
		t.Run(target, func(t *testing.T) {
			if target == "interp" {
				e2eharness.CheckReadLineText(t, func() *exec.Cmd { return exec.Command(fern, "-interp", src) }, nil)
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
			bin := filepath.Join(t.TempDir(), "lines")
			args := []string{"-target", actual, "-o", bin}
			if target == "wasm32-wasi" {
				args = append(args, "-emit", "command-module")
			}
			args = append(args, src)
			if out, err := exec.Command(fern, args...).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			argv := append(append([]string{}, runner...), bin)
			e2eharness.CheckReadLineText(t, func() *exec.Cmd { return exec.Command(argv[0], argv[1:]...) }, nil)
		})
	}
}
