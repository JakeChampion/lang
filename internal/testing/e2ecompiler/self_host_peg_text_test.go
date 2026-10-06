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

func TestSelfHostPegText(t *testing.T) {
	cli := buildSelfHostCLI(t)
	t.Run("interp", func(t *testing.T) {
		cmd := runX86_64Bin(cli.runner, cli.bin, "-interp", e2eharness.WritePegTextFixture(t), cli.stdlib)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("interpreter: %v\n%s", err, out)
		}
	})
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, e2eharness.WritePegTextFixture(t), target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit = %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
	t.Run("component", func(t *testing.T) {
		checkPegTextComponent(t, cli.bin, cli.runner, cli.stdlib)
	})
}

func checkPegTextComponent(t *testing.T, compiler string, runner []string, stdlib string) {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("requires wasmtime")
	}
	bin := filepath.Join(t.TempDir(), "peg-text.wasm")
	cmd := runX86_64Bin(runner, compiler, "-target", "wasm32-wasi", "-o", bin, e2eharness.WritePegTextFixture(t), stdlib)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("component compile: %v\n%s", err, out)
	}
	// Components have no exit-time census; native and core legs check it.
	if out, err := exec.Command("wasmtime", "run", bin).CombinedOutput(); err != nil {
		t.Fatalf("component run: %v\n%s", err, out)
	}
}

func TestSelfHostPegExample(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			src := langSrcAbs(t, "tests/stdlib/peg_test.fern")
			var cmd *exec.Cmd
			switch target {
			case "x86-64-linux":
				cmd = runX86_64Bin(cli.runner, cli.x86Binary(t, src))
			case "arm64-linux":
				gcc, qemu := arm64Tooling(t)
				asm, err := os.ReadFile(cli.emit(t, src, target))
				if err != nil {
					t.Fatal(err)
				}
				cmd = runArm64Bin(qemu, buildBinArm64(t, gcc, t.TempDir(), "peg", string(asm)))
			case "wasm32-wasi":
				cmd = exec.Command(e2eharness.Wasmtime(t), "run", cli.emit(t, src, target))
			}
			output, err := cmd.CombinedOutput()
			out := string(output)
			if err != nil || !strings.Contains(out, "# pass 18") || !strings.Contains(out, "# fail 0") || !strings.Contains(out, "1..18") {
				t.Fatalf("existing PEG suite: %v\n%s", err, out)
			}
		})
	}
}

func TestSelfHostArm64DarwinPegText(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	src := e2eharness.WritePegTextFixture(t)
	if out, err := exec.Command(cli, "-interp", src, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("interpreter: %v\n%s", err, out)
	}
	bin := filepath.Join(t.TempDir(), "peg-text")
	compile := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, stdlib)
	compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	} else {
		assertBalancedCensus(t, string(out))
	}
	t.Run("component", func(t *testing.T) { checkPegTextComponent(t, cli, nil, stdlib) })
}
