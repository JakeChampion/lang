package e2e

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"testing/iotest"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/e2eharness"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// Exercise the actual stdlib with a host reader that fails after delivering
// data. Neither a valid prefix nor an invalid prefix may hide the I/O error.
func TestIOTextPartialReadError(t *testing.T) {
	prog, _, err := modload.LoadSource(`import "std/io";
function main(): i32 {
    match (io.read_all_stdin()) {
        Ok(_) => { return 1; },
        Err(e) => { match (e) { Other(_, _) => { return 0; }, _ => { return 2; } } },
    }
}`)
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
	for _, tc := range []struct{ name, prefix string }{
		{"valid", "valid prefix"},
		{"invalid", "invalid\xffprefix"},
		{"multiple chunks", strings.Repeat("a", 8193)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := interp.New()
			i.SetDynCoercions(info.DynCoercions)
			i.Stdin = io.MultiReader(strings.NewReader(tc.prefix), iotest.ErrReader(syscall.EIO))
			for _, ed := range prog.Enums {
				i.RegisterEnum(ed)
			}
			for _, fn := range prog.Funcs {
				i.Register(fn)
			}
			got, err := i.CallByName("main", nil)
			if err != nil || got != interp.Number(0) {
				t.Fatalf("main = %v, %v; want Other I/O error", got, err)
			}
		})
	}
}

func TestIOTextReadError(t *testing.T) {
	source := e2eharness.IOTextReadErrorProgram
	t.Run("interp", func(t *testing.T) {
		if code := runInterpExit(t, source); code != 0 {
			t.Fatalf("exit = %d", code)
		}
	})
	t.Run("x86", func(t *testing.T) {
		if out, code := compileAndRunX86_64(t, source); code != 0 {
			t.Fatalf("exit = %d\n%s", code, out)
		}
	})
	t.Run("arm", func(t *testing.T) {
		if out, code := compileAndRunArm64(t, source); code != 0 {
			t.Fatalf("exit = %d\n%s", code, out)
		}
	})
	t.Run("darwin", func(t *testing.T) {
		if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
			t.Skip("requires Apple Silicon")
		}
		if code := runArm64Darwin(t, source); code != 0 {
			t.Fatalf("exit = %d", code)
		}
	})
}

func TestIOText(t *testing.T) {
	fern := buildLangBinForInterp(t)
	for _, call := range []struct{ name, expr string }{{"stdin", "io.read_all_stdin()"}, {"dash", `io.read_input("-")`}, {"empty", `io.read_input("")`}} {
		for _, target := range []string{"interp", "arm64-darwin", "x86-64-linux", "arm64-linux", "wasm32-wasi", "wasm32-preview2"} {
			t.Run(call.name+"/"+target, func(t *testing.T) {
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
				if err := os.WriteFile(src, []byte(e2eharness.IOTextProgram(call.expr)), 0o644); err != nil {
					t.Fatal(err)
				}
				var prefix []string
				if target == "interp" {
					prefix = []string{fern, "-interp", src}
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
					prefix = append(append([]string{}, runner...), bin)
				}
				invalidExit := 65
				if target == "wasm32-preview2" {
					invalidExit = 1
				}
				e2eharness.CheckIOText(t, func() *exec.Cmd { return exec.Command(prefix[0], prefix[1:]...) }, false, invalidExit)
			})
		}
	}
}
