package e2e

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestExampleTeeBytes(t *testing.T) {
	fern := buildLangBinForInterp(t)
	src, err := filepath.Abs("../../examples/cli/tee.fern")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"interp", "arm64-darwin", "x86-64-linux", "arm64-linux", "wasm32-wasi", "wasm32-preview2"} {
		t.Run(target, func(t *testing.T) {
			var runner []string
			switch target {
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
				runner = []string{"wasmtime", "run", "--dir=."}
			}
			var prefix []string
			if target == "interp" {
				prefix = []string{fern, "-interp", src, "--"}
			} else {
				bin := filepath.Join(t.TempDir(), "tee")
				compileTarget := target
				if target == "wasm32-preview2" {
					compileTarget = "wasm32-wasi"
				}
				args := []string{"-target", compileTarget, "-o", bin, src}
				if target == "wasm32-wasi" {
					args = append([]string{"-emit", "command-module"}, args...)
				}
				if out, err := exec.Command(fern, args...).CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, out)
				}
				prefix = append(append([]string{}, runner...), bin)
			}
			e2eharness.CheckExampleTee(t, func(args ...string) *exec.Cmd {
				argv := append(append([]string{}, prefix...), args...)
				return exec.Command(argv[0], argv[1:]...)
			}, false)
		})
	}
}
