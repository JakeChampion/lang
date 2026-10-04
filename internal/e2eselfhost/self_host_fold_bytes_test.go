package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostFoldBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../coreutils/fold.fern")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
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
				bin = buildBinArm64(t, gcc, t.TempDir(), "fold", string(asm))
				if qemu != "" {
					runner = []string{qemu}
				}
			case "wasm32-wasi":
				bin = cli.emit(t, src, target, env...)
				runner = []string{"wasmtime", "run", "--dir=."}
			}
			e2eharness.RunFoldByteCases(t, bin, runner, assertBalancedCensus)
		})
	}
}

func TestSelfHostArm64DarwinFoldBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	src, err := filepath.Abs("../../coreutils/fold.fern")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "fold")
	cmd := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = append(os.Environ(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	e2eharness.RunFoldByteCases(t, bin, nil, assertBalancedCensus)
}
