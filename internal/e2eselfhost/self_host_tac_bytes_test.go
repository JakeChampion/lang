package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostTacBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../coreutils/tac.fern")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var bin string
			var runner []string
			env := []string{"FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}
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
				bin = buildBinArm64(t, gcc, t.TempDir(), "tac", string(asm))
				if qemu != "" {
					runner = []string{qemu}
				}
			case "wasm32-wasi":
				bin = cli.emit(t, src, target, env...)
				runner = []string{"wasmtime", "run", "--dir=."}
			}
			e2eharness.RunTacByteCases(t, bin, runner, assertBalancedCensus)
		})
	}
}

func TestSelfHostArm64DarwinTacBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	src, err := filepath.Abs("../../coreutils/tac.fern")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "tac")
	cmd := exec.Command(cli, "-target", "arm64-darwin", src, e2eharness.SelfHostStdlibRoot(t), "-o", bin)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	e2eharness.RunTacByteCases(t, bin, nil, assertBalancedCensus)
}
