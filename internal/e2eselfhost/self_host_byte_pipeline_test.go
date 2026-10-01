package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostBytePipeline(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "pipeline.fern")
	if err := os.WriteFile(src, []byte(e2eharness.BytePipelineProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{"FERN_SEM_IR=1", "FERN_SEM_IR_STRICT=1", "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var binary string
			var runner []string
			switch target {
			case "x86-64-linux":
				binary, runner = cli.x86Binary(t, src, env...), cli.runner
			case "arm64-linux":
				gcc, qemu := arm64Tooling(t)
				asm, err := os.ReadFile(cli.emit(t, src, target, env...))
				if err != nil {
					t.Fatal(err)
				}
				binary = buildBinArm64(t, gcc, t.TempDir(), "pipeline", string(asm))
				if qemu != "" {
					runner = []string{qemu}
				}
			case "wasm32-wasi":
				binary, runner = cli.emit(t, src, target, env...), []string{"wasmtime", "run"}
			}
			for _, input := range [][]byte{nil, bytes.Repeat(e2eharness.ReaderBytesInput(), 8)} {
				assertBalancedCensus(t, e2eharness.CheckBytePipeline(t, runX86_64Bin(runner, binary), input))
			}
		})
	}
}

func TestSelfHostArm64DarwinBytePipeline(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	src, bin := filepath.Join(dir, "pipeline.fern"), filepath.Join(t.TempDir(), "pipeline")
	if err := os.WriteFile(src, []byte(e2eharness.BytePipelineProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cli, "-target", "arm64-darwin", src, e2eharness.SelfHostStdlibRoot(t), "-o", bin)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR=1", "FERN_SEM_IR_STRICT=1", "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	for _, input := range [][]byte{nil, bytes.Repeat(e2eharness.ReaderBytesInput(), 8)} {
		assertBalancedCensus(t, e2eharness.CheckBytePipeline(t, exec.Command(bin), input))
	}
}
