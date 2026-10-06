package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestMismatchBytes(t *testing.T) {
	fern := buildLangBinForInterp(t)
	src := filepath.Join(t.TempDir(), "scan.fern")
	if err := os.WriteFile(src, []byte(e2eharness.MismatchBytesSource(true)), 0o644); err != nil {
		t.Fatal(err)
	}
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
				bin := filepath.Join(t.TempDir(), "scan")
				if actual == "wasm32-preview2" {
					actual = "wasm32-wasi"
				}
				args := []string{"-target", actual, "-o", bin, src}
				if target == "wasm32-wasi" {
					args = append([]string{"-emit", "command-module"}, args...)
				}
				if out, err := exec.Command(fern, args...).CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, out)
				}
				argv = append(append([]string{}, runner...), bin)
			}
			if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
		})
	}
}
