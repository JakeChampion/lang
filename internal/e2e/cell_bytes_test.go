package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestCellBytes(t *testing.T) {
	fern := buildLangBinForInterp(t)
	src := e2eharness.WriteCellBytesFixture(t)
	for _, target := range []string{"interp", "arm64-darwin", "x86-64-linux", "arm64-linux", "wasm32-wasi", "wasm32-preview2"} {
		t.Run(target, func(t *testing.T) {
			var runner []string
			actual := target
			switch actual {
			case "arm64-darwin":
				if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
					t.Skip("requires Apple Silicon")
				}
			case "x86-64-linux":
				_, runner = x86_64Tooling(t)
			case "arm64-linux":
				_, qemu := arm64Tooling(t)
				if qemu != "" {
					runner = []string{qemu}
				}
			case "wasm32-wasi", "wasm32-preview2":
				if _, err := exec.LookPath("wasmtime"); err != nil {
					t.Skip("requires wasmtime")
				}
				runner = []string{"wasmtime", "run"}
			}
			var argv []string
			if actual == "interp" {
				argv = []string{fern, "-interp", src}
			} else {
				bin := filepath.Join(t.TempDir(), "cell-bytes")
				if actual == "wasm32-preview2" {
					actual = "wasm32-wasi"
				}
				args := []string{"-target", actual, "-o", bin, src}
				if target == "wasm32-wasi" {
					args = append([]string{"-emit", "command-module"}, args...)
				}
				cmd := exec.Command(fern, args...)
				cmd.Env = append(os.Environ(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, out)
				}
				argv = append(append([]string{}, runner...), bin)
			}
			if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			} else if target != "interp" && target != "wasm32-preview2" {
				allocs, frees, live := parseLeakCheckLine(t, string(out))
				if allocs != frees || live != 0 {
					t.Fatalf("unbalanced cell ownership: %s", out)
				}
			}
		})
	}
}
