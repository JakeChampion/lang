package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostSortComparatorBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	t.Run("interp", func(t *testing.T) {
		src := e2eharness.WriteSortComparatorBytesFixture(t)
		out, diagnostic, code := runInterpCLI(t, runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib), "")
		if code != 0 || out != "" {
			t.Fatalf("interpreter: exit %d, stdout %q, stderr %q", code, out, diagnostic)
		}
	})
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, e2eharness.WriteSortComparatorBytesFixture(t), target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit = %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostArm64DarwinSortComparatorBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	src := e2eharness.WriteSortComparatorBytesFixture(t)
	t.Run("native", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "sort-comparator")
		compile := exec.Command(cli, "-target", "arm64-darwin", src, stdlib, "-o", bin)
		compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
		if out, err := compile.CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		if out, err := exec.Command(bin).CombinedOutput(); err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		} else {
			assertBalancedCensus(t, string(out))
		}
	})
	t.Run("interp", func(t *testing.T) {
		if out, err := exec.Command(cli, "-interp", src, stdlib).CombinedOutput(); err != nil {
			t.Fatalf("interpreter: %v\n%s", err, out)
		}
	})
}
