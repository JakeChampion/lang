package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostShredBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../coreutils/shred.fern")
	if err != nil {
		t.Fatal(err)
	}
	// The complete utility requires fsmode (chmod), unavailable on WASI.
	// Its byte-pattern kernel is covered on WASI by the separate test below.
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
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
				bin = buildBinArm64(t, gcc, t.TempDir(), "shred", string(asm))
				if qemu != "" {
					runner = []string{qemu}
				}
			}
			e2eharness.RunShredByteCases(t, bin, runner, assertBalancedCensus)
		})
	}
}

func TestSelfHostShredPatternBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := e2eharness.WriteShredPatternFixture(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, src, target, nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostArm64DarwinShredPatternBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	src := e2eharness.WriteShredPatternFixture(t)
	bin := filepath.Join(t.TempDir(), "patterns")
	cmd := exec.Command(cli, "-target", "arm64-darwin", src, e2eharness.SelfHostStdlibRoot(t), "-o", bin)
	cmd.Env = append(os.Environ(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	assertBalancedCensus(t, string(out))
}

func TestSelfHostArm64DarwinShredBytes(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	src, err := filepath.Abs("../../coreutils/shred.fern")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "shred")
	cmd := exec.Command(cli, "-target", "arm64-darwin", src, e2eharness.SelfHostStdlibRoot(t), "-o", bin)
	cmd.Env = append(os.Environ(), "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	e2eharness.RunShredByteCases(t, bin, nil, assertBalancedCensus)
}
