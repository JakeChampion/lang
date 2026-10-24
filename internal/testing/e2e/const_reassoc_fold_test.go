package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const constReassocFoldSrc = `function f(x: i32): i32 { return x + 1 + 2; }
function h(x: i32): i32 { return (x & 12) & 10; }
function o(x: i32): i32 { return x | 1 | 4; }
function m(x: i32): i32 { return x * 3 * 5; }
function chain(x: i32): i32 { return x + 1 + 2 + 3; }
function main(): i32 {
    return f(10) + h(15) + o(0) + m(2) + chain(0);
}
`

func TestInterpConstReassocFold(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(constReassocFoldSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 62 {
		t.Errorf("exit = %d, want 62\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64ConstReassocFold(t *testing.T) {
	out, code := compileAndRunX86_64(t, constReassocFoldSrc)
	if code != 62 {
		t.Errorf("exit = %d, want 62\n%s", code, out)
	}
}

func TestArm64ConstReassocFold(t *testing.T) {
	out, code := compileAndRunArm64(t, constReassocFoldSrc)
	if code != 62 {
		t.Errorf("exit = %d, want 62\n%s", code, out)
	}
}

func TestWASMConstReassocFold(t *testing.T) {
	if code := runWasm(t, constReassocFoldSrc); code != 62 {
		t.Errorf("wasm exit = %d, want 62", code)
	}
}

const constReassocFoldI64Src = `function big(x: i64): i64 { return x + 10i64 + 20i64; }
function mask(x: i64): i64 { return (x & 255i64) & 15i64; }
function main(): i32 {
    return (big(5i64) + mask(255i64)) as i32;
}
`

func TestX86_64ConstReassocFoldI64(t *testing.T) {
	out, code := compileAndRunX86_64(t, constReassocFoldI64Src)
	if code != 50 {
		t.Errorf("exit = %d, want 50\n%s", code, out)
	}
}

func TestWASMConstReassocFoldI64(t *testing.T) {
	if code := runWasm(t, constReassocFoldI64Src); code != 50 {
		t.Errorf("wasm exit = %d, want 50", code)
	}
}
