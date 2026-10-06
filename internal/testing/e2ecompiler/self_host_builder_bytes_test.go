package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostBuilderBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, e2eharness.BuilderBytesProgram, target, "FERN_STRICT_IR=1"); code != 0 {
				t.Fatalf("exit = %d, want 0\n%s", code, stderr)
			}
		})
	}
}

func TestSelfHostInterpBuilderBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	path := filepath.Join(t.TempDir(), "builder.fern")
	if err := os.WriteFile(path, []byte(e2eharness.BuilderBytesProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runInterpCLI(t, runX86_64Bin(cli.runner, cli.bin, "-interp", path, cli.stdlib), "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

func TestSelfHostBuilderBytesOwnership(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, e2eharness.BuilderBytesProgram, target, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 || strings.Contains(stderr, "fern-sanitizer:") {
				t.Fatalf("exit = %d, want 0 without a sanitizer finding\n%s", code, stderr)
			}
			if !strings.Contains(stderr, "leakcheck:") || !strings.Contains(stderr, "live_bytes=0") {
				t.Fatalf("missing zero-live-byte heap census\n%s", stderr)
			}
		})
	}
}

func TestSelfHostArm64DarwinBuilderBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	src := filepath.Join(t.TempDir(), "bytes.fern")
	if err := os.WriteFile(src, []byte(e2eharness.BuilderBytesProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "bytes")
	cmd := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	assertBalancedCensus(t, string(out))
	if out, err := exec.Command(cli, "-interp", src, e2eharness.SelfHostStdlibRoot(t)).CombinedOutput(); err != nil {
		t.Fatalf("interpreter: %v\n%s", err, out)
	}
}
