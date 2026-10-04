package e2e

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/e2eharness"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

type byteLineReadFailure struct{ calls int }

func (r *byteLineReadFailure) Read([]byte) (int, error) {
	r.calls++
	return 0, syscall.EIO
}

func TestIOByteLinesPartialReadError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  byte
		length int
	}{{"ascii", 'a', 17}, {"invalid UTF-8", 255, 17}, {"long binary tail", 128, 8193}} {
		t.Run(tc.name, func(t *testing.T) {
			prog, _, err := modload.LoadSource(e2eharness.IOByteLineReadErrorProgram(false, 7, tc.length, int(tc.value)))
			if err != nil {
				t.Fatal(err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatal(err)
			}
			if err := monomorph.Run(prog, info); err != nil {
				t.Fatal(err)
			}
			failure := &byteLineReadFailure{}
			i := interp.New()
			i.SetDynCoercions(info.DynCoercions)
			i.Stdin = io.MultiReader(bytes.NewReader(bytes.Repeat([]byte{tc.value}, tc.length)), failure)
			for _, ed := range prog.Enums {
				i.RegisterEnum(ed)
			}
			for _, fn := range prog.Funcs {
				i.Register(fn)
			}
			got, err := i.CallByName("main", nil)
			if err != nil || got != interp.Number(0) {
				t.Fatalf("main = %v, %v; want partial bytes and sticky I/O error", got, err)
			}
			if failure.calls != 1 {
				t.Fatalf("erroring host reader called %d times; want 1", failure.calls)
			}
		})
	}
}

func TestIOByteLines(t *testing.T) {
	fern := buildLangBinForInterp(t)
	for _, config := range e2eharness.IOByteLineCases() {
		for _, target := range []string{"interp", "arm64-darwin", "x86-64-linux", "arm64-linux", "wasm32-wasi", "wasm32-preview2"} {
			t.Run(config.Name+"/"+target, func(t *testing.T) {
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
					runner = []string{"wasmtime", "run"}
				}
				src := filepath.Join(t.TempDir(), "main.fern")
				if err := os.WriteFile(src, []byte(config.Source), 0o644); err != nil {
					t.Fatal(err)
				}
				var argv []string
				if target == "interp" {
					argv = []string{fern, "-interp", src}
				} else {
					bin := filepath.Join(t.TempDir(), "reader")
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
					argv = append(append([]string{}, runner...), bin)
				}
				config.Check(t, func() *exec.Cmd { return exec.Command(argv[0], argv[1:]...) }, false)
			})
		}
	}
}
