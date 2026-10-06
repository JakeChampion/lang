package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestBytePipelineInterp(t *testing.T) {
	src := filepath.Join(t.TempDir(), "pipeline.fern")
	if err := os.WriteFile(src, []byte(e2eharness.BytePipelineProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	fern := buildLangBinForInterp(t)
	for _, input := range [][]byte{nil, bytes.Repeat(e2eharness.ReaderBytesInput(), 8)} {
		e2eharness.CheckBytePipeline(t, exec.Command(fern, "-interp", src), input)
	}
}

func TestArm64DarwinBytePipeline(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	src, bin := filepath.Join(t.TempDir(), "pipeline.fern"), filepath.Join(t.TempDir(), "pipeline")
	if err := os.WriteFile(src, []byte(e2eharness.BytePipelineProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(buildLangBinForInterp(t), "-target", "arm64-darwin", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	for _, input := range [][]byte{nil, bytes.Repeat(e2eharness.ReaderBytesInput(), 8)} {
		e2eharness.CheckBytePipeline(t, exec.Command(bin), input)
	}
}
