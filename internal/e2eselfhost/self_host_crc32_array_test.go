package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostCRC32Array(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, e2eharness.CRC32ArraySource(true), target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit = %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostCRC32ArrayInterpAndComponent(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src, bin := filepath.Join(dir, "crc.fern"), filepath.Join(dir, "crc.wasm")
	if err := os.WriteFile(src, []byte(e2eharness.CRC32ArraySource(false)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("interp", func(t *testing.T) {
		if out, err := runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("interpreter: %v\n%s", err, out)
		}
	})
	t.Run("component", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("requires wasmtime")
		}
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", src, cli.stdlib, "-o", bin)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("component build: %v\n%s", err, out)
		}
		if out, err := exec.Command("wasmtime", "run", bin).CombinedOutput(); err != nil {
			t.Fatalf("component run: %v\n%s", err, out)
		}
	})
}

func TestSelfHostArm64DarwinCRC32Array(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	src := filepath.Join(dir, "reduction.fern")
	if err := os.WriteFile(src, []byte(e2eharness.CRC32ArraySource(true)), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "reduction")
	compile := exec.Command(cli, "-target", "arm64-darwin", src, stdlib, "-o", bin)
	compile.Env = append(os.Environ(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	} else {
		assertBalancedCensus(t, string(out))
	}
	t.Run("interp", func(t *testing.T) {
		if err := os.WriteFile(src, []byte(e2eharness.CRC32ArraySource(false)), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(cli, "-interp", src, stdlib).CombinedOutput(); err != nil {
			t.Fatalf("interpreter: %v\n%s", err, out)
		}
	})
}
