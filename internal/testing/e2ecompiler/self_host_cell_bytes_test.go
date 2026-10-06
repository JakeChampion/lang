package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostCellBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	t.Run("pin-built-interp", func(t *testing.T) {
		cmd := runX86_64Bin(cli.runner, cli.bin, "-interp", e2eharness.WriteCellBytesFixture(t), cli.stdlib)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("pin-built interpreter: %v\n%s", err, out)
		}
	})
	t.Run("interp", func(t *testing.T) {
		dir := t.TempDir()
		copySelfHostDriver(t, dir, "drivers/interp_run.fern")
		bin := filepath.Join(dir, "interp")
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-o", bin, filepath.Join(dir, "drivers/interp_run.fern"), cli.stdlib)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compile interpreter: %v\n%s", err, out)
		}
		cmd = runX86_64Bin(cli.runner, bin)
		cmd.Stdin = bytes.NewBufferString(e2eharness.CellBytesSource)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("interpreter: %v\n%s", err, out)
		}
	})
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, e2eharness.WriteCellBytesFixture(t), target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit = %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
	t.Run("component", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("requires wasmtime")
		}
		bin := filepath.Join(t.TempDir(), "cell-bytes.wasm")
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-o", bin, e2eharness.WriteCellBytesFixture(t), cli.stdlib)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("component compile: %v\n%s", err, out)
		}
		if out, err := exec.Command("wasmtime", "run", bin).CombinedOutput(); err != nil {
			t.Fatalf("component run: %v\n%s", err, out)
		}
	})
}

func TestSelfHostPerModuleCellBytes(t *testing.T) {
	leaf := strings.Replace(e2eharness.CellBytesSource, "function main():", "pub function save():", 1)
	checkSelfHostPerModuleByteSink(t, leaf, nil)
}

func TestSelfHostArm64DarwinCellBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	src := e2eharness.WriteCellBytesFixture(t)
	if out, err := exec.Command(cli, "-interp", src, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("pin-built interpreter: %v\n%s", err, out)
	}
	bin := filepath.Join(t.TempDir(), "cell-bytes")
	cmd := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, stdlib)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	} else {
		assertBalancedCensus(t, string(out))
	}
}
