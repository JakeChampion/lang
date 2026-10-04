package e2e

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestDircolorsRawBytes(t *testing.T) {
	fern := buildLangBinForInterp(t)
	src, err := filepath.Abs("../../coreutils/dircolors.fern")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"interp", "arm64-darwin", "x86-64-linux", "arm64-linux", "wasm32-wasi", "wasm32-preview2"} {
		t.Run(target, func(t *testing.T) {
			if target == "interp" {
				// The CLI separates program arguments from its source with --.
				e2eharness.RunDircolorsByteCases(t, "--", []string{fern, "-interp", src}, nil)
				return
			}
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
				runner = []string{"wasmtime", "run", "--env", "TERM=linux", "--env", "COLORTERM="}
				actual = "wasm32-wasi"
			}
			bin := filepath.Join(t.TempDir(), "dircolors")
			args := []string{"-target", actual, "-o", bin}
			if target == "wasm32-wasi" {
				args = append(args, "-emit", "command-module")
			}
			cmd := exec.Command(fern, append(args, src)...)
			cmd.Env = e2eharness.ChildEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			e2eharness.RunDircolorsByteCases(t, bin, runner, nil)
		})
	}
}
