package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostExampleTeeBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../examples/cli/tee.fern")
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
				bin = buildBinArm64(t, gcc, t.TempDir(), "tee", string(asm))
				if qemu != "" {
					runner = []string{qemu}
				}
			case "wasm32-wasi":
				bin = cli.emit(t, src, target, env...)
				runner = []string{"wasmtime", "run", "--dir=."}
			}
			e2eharness.CheckExampleTee(t, func(args ...string) *exec.Cmd {
				argv := append(append(append([]string{}, runner...), bin), args...)
				return exec.Command(argv[0], argv[1:]...)
			}, true)
		})
	}
}
