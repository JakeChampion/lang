package e2e

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestExampleTrBytes(t *testing.T) {
	fern := buildLangBinForInterp(t)
	for _, target := range []string{"arm64-darwin", "x86-64-linux", "arm64-linux", "wasm32-wasi"} {
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
			case "wasm32-wasi":
				if _, err := exec.LookPath("wasmtime"); err != nil {
					t.Skip("requires wasmtime")
				}
				runner = []string{"wasmtime", "run"}
			}
			bin := filepath.Join(t.TempDir(), "tr")
			args := []string{"-target", target, "-o", bin, "../../../examples/cli/tr.fern"}
			if target == "wasm32-wasi" {
				args = append([]string{"-emit", "command-module"}, args...)
			}
			if out, err := exec.Command(fern, args...).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			e2eharness.CheckExampleTr(t, func(args ...string) *exec.Cmd {
				argv := append(append(append([]string{}, runner...), bin), args...)
				return exec.Command(argv[0], argv[1:]...)
			}, false)
		})
	}
}
