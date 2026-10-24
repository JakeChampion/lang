package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const arrowInferSrc = `function apply_i(f: (i32) => i32, x: i32): i32 { return f(x); }
function keep(f: (i32) => boolean, x: i32): i32 { if (f(x)) { return 10; } return 0; }
function main(): i32 {
    let a: i32 = apply_i((x: i32) => x * 2, 16);
    let b: i32 = keep((x: i32) => x > 5, 9);
    return a + b;
}
`

func TestInterpArrowReturnInfer(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(arrowInferSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("exit = %d, want 42\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64ArrowReturnInfer(t *testing.T) {
	if out, code := compileAndRunX86_64(t, arrowInferSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestArm64ArrowReturnInfer(t *testing.T) {
	if out, code := compileAndRunArm64(t, arrowInferSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestWASMArrowReturnInfer(t *testing.T) {
	if code := runWasm(t, arrowInferSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}
