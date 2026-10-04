package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostIOText(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run("read error/"+target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, e2eharness.IOTextReadErrorProgram, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit = %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
	for _, call := range []struct{ name, expr string }{{"stdin", "io.read_all_stdin()"}, {"dash", `io.read_input("-")`}, {"empty", `io.read_input("")`}} {
		src := filepath.Join(t.TempDir(), "reader.fern")
		if err := os.WriteFile(src, []byte(e2eharness.IOTextProgram(call.expr)), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(call.name+"/"+target, func(t *testing.T) {
				var bin string
				var runner []string
				env := []string{"FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}
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
				e2eharness.CheckIOText(t, func() *exec.Cmd { return exec.Command(argv[0], argv[1:]...) }, true, 65)
			})
		}
	}
}

func TestSelfHostIOTextComponent(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("requires wasmtime")
	}
	cli := buildSelfHostCLI(t)
	for _, call := range []struct{ name, expr string }{{"stdin", "io.read_all_stdin()"}, {"dash", `io.read_input("-")`}, {"empty", `io.read_input("")`}} {
		t.Run(call.name, func(t *testing.T) {
			dir := t.TempDir()
			src, bin := filepath.Join(dir, "reader.fern"), filepath.Join(dir, "reader.wasm")
			if err := os.WriteFile(src, []byte(e2eharness.IOTextProgram(call.expr)), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-o", bin, src, cli.stdlib)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("component build: %v\n%s", err, out)
			}
			e2eharness.CheckIOText(t, func() *exec.Cmd { return exec.Command("wasmtime", "run", bin) }, false, 1)
		})
	}
}

func TestSelfHostIOTextInterp(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "reader.fern")
	if err := os.WriteFile(src, []byte(e2eharness.IOTextProgram("io.read_all_stdin()")), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eharness.CheckIOText(t, func() *exec.Cmd { return runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib) }, false, 65)
}

func TestSelfHostArm64DarwinIOText(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	src := filepath.Join(dir, "reader.fern")
	if err := os.WriteFile(src, []byte(e2eharness.IOTextProgram("io.read_all_stdin()")), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "reader")
	compile := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, stdlib)
	compile.Env = append(os.Environ(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	t.Run("compiled", func(t *testing.T) { e2eharness.CheckIOText(t, func() *exec.Cmd { return exec.Command(bin) }, true, 65) })
	t.Run("interp", func(t *testing.T) {
		e2eharness.CheckIOText(t, func() *exec.Cmd { return exec.Command(cli, "-interp", src, stdlib) }, false, 65)
	})
}
