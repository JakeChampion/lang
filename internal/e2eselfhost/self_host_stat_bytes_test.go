package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostStatBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../coreutils/stat.fern")
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
				bin = buildBinArm64(t, gcc, t.TempDir(), "stat", string(asm))
				if qemu != "" {
					runner = []string{qemu}
				}
			case "wasm32-wasi":
				// stat requires cwd, permission and filesystem metadata that
				// WASI does not provide. Preserve the explicit capability refusal.
				cmd := runX86_64Bin(cli.runner, cli.bin, "-target", target, src, cli.stdlib)
				cmd.Env = append(os.Environ(), env...)
				out, err := cmd.CombinedOutput()
				if err == nil {
					t.Fatal("stat unexpectedly compiled for WASI")
				}
				for _, capability := range []string{"cwd", "fsmode", "fsinfo"} {
					if !strings.Contains(string(out), "error[E066]: target \"wasm32-wasi\" does not provide `"+capability+"`") {
						t.Fatalf("missing %s refusal: %s", capability, out)
					}
				}
				return
			}
			e2eharness.RunStatByteCases(t, bin, runner, assertBalancedCensus)
		})
	}
}

func TestSelfHostArm64DarwinStatBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	src, err := filepath.Abs("../../coreutils/stat.fern")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "stat")
	cmd := exec.Command(cli, "-target", "arm64-darwin", src, e2eharness.SelfHostStdlibRoot(t), "-o", bin)
	cmd.Env = append(os.Environ(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	e2eharness.RunStatByteCases(t, bin, nil, assertBalancedCensus)
}
