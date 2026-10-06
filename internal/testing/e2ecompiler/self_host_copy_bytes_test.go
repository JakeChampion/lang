package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostCopyBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, utility := range []string{"cp", "install", "mv"} {
		src, err := filepath.Abs("../../../coreutils/" + utility + ".fern")
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"x86-64-linux", "arm64-linux"} {
			t.Run(utility+"/"+target, func(t *testing.T) {
				env := []string{"FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}
				var bin string
				var runner []string
				if target == "x86-64-linux" {
					bin = cli.x86Binary(t, src, env...)
					runner = cli.runner
				} else {
					gcc, qemu := arm64Tooling(t)
					asm, err := os.ReadFile(cli.emit(t, src, target, env...))
					if err != nil {
						t.Fatal(err)
					}
					bin = buildBinArm64(t, gcc, t.TempDir(), utility, string(asm))
					if qemu != "" {
						runner = []string{qemu}
					}
				}
				e2eharness.RunCopyByteCases(t, utility, bin, runner, assertBalancedCensus)
			})
		}
	}
}

func TestSelfHostArm64DarwinCopyBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	for _, utility := range []string{"cp", "install"} {
		t.Run(utility, func(t *testing.T) {
			src, err := filepath.Abs("../../../coreutils/" + utility + ".fern")
			if err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(t.TempDir(), utility)
			cmd := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, e2eharness.SelfHostStdlibRoot(t))
			cmd.Env = append(os.Environ(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			e2eharness.RunCopyByteCases(t, utility, bin, nil, assertBalancedCensus)
		})
	}
}
