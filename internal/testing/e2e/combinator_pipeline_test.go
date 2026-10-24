package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const combinatorPipelineSrc = `import "std/array";
function main(): i32 {
    return [1, 2, 3, 4, 5]
        .map((x: i32) => x * 2)
        .filter((x: i32) => x > 4)
        .fold(0, (acc: i32, x: i32) => acc + x);
}
`

func TestInterpCombinatorPipeline(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(combinatorPipelineSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 24 {
		t.Errorf("exit = %d, want 24\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestArm64CombinatorPipeline(t *testing.T) {
	if out, code := compileAndRunArm64(t, combinatorPipelineSrc); code != 24 {
		t.Errorf("exit = %d, want 24\n%s", code, out)
	}
}

func TestWASMCombinatorPipeline(t *testing.T) {
	if code := runWasm(t, combinatorPipelineSrc); code != 24 {
		t.Errorf("wasm exit = %d, want 24", code)
	}
}
