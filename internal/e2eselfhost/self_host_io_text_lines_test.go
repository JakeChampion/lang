package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostIOTextLines(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, config := range e2eharness.IOTextLineCases() {
		src := filepath.Join(t.TempDir(), "reader.fern")
		if err := os.WriteFile(src, []byte(config.Source), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(config.Name+"/"+target, func(t *testing.T) {
				var bin string
				var runner []string
				env := []string{"FERN_SEM_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}
				switch target {
				case "x86-64-linux":
					bin = cli.x86Binary(t, src, env...)
					runner = cli.runner
				case "arm64-linux":
					gcc, qemu := arm64Tooling(t)
					asm, err := os.ReadFile(cli.emit(t, src, target, env...))
					if err != nil {
						t.Fatal(err)
					}
					bin = buildBinArm64(t, gcc, t.TempDir(), "reader", string(asm))
					if qemu != "" {
						runner = []string{qemu}
					}
				case "wasm32-wasi":
					bin = cli.emit(t, src, target, env...)
					runner = []string{"wasmtime", "run"}
				}
				argv := append(append([]string{}, runner...), bin)
				config.Check(t, func() *exec.Cmd { return exec.Command(argv[0], argv[1:]...) }, true)
			})
		}
	}
}

func TestSelfHostArm64DarwinIOTextLines(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	for _, config := range e2eharness.IOTextLineCases() {
		t.Run(config.Name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "reader.fern")
			if err := os.WriteFile(src, []byte(config.Source), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(t.TempDir(), "reader")
			compile := exec.Command(cli, "-target", "arm64-darwin", src, stdlib, "-o", bin)
			compile.Env = append(os.Environ(), "FERN_SEM_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			t.Run("compiled", func(t *testing.T) {
				config.Check(t, func() *exec.Cmd { return exec.Command(bin) }, true)
			})
			// Seven-byte chunks exercise scalar splits and multi-chunk records
			// without repeating every chunk size in the tree interpreter.
			if config.Name == "alternating/7" {
				t.Run("interp", func(t *testing.T) {
					config.Check(t, func() *exec.Cmd { return exec.Command(cli, "-interp", src, stdlib) }, false)
				})
			}
		})
	}
}
