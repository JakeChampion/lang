package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const unaryNegSrc = `struct V { x: i32 }
function (self: V) neg(): V { return V { x: 0 - self.x }; }
function main(): i32 {
    let a: V = V { x: 5 };
    let b: V = -a;
    return b.x + 100;
}
`

func TestInterpUnaryNegOverload(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(unaryNegSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 95 {
		t.Errorf("exit = %d, want 95\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64UnaryNegOverload(t *testing.T) {
	out, code := compileAndRunX86_64(t, unaryNegSrc)
	if code != 95 {
		t.Errorf("exit = %d, want 95\n%s", code, out)
	}
}

func TestArm64UnaryNegOverload(t *testing.T) {
	out, code := compileAndRunArm64(t, unaryNegSrc)
	if code != 95 {
		t.Errorf("exit = %d, want 95\n%s", code, out)
	}
}

func TestWASMUnaryNegOverload(t *testing.T) {
	if code := runWasm(t, unaryNegSrc); code != 95 {
		t.Errorf("wasm exit = %d, want 95", code)
	}
}
