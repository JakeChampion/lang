package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

const i32OverflowWrapProgram = `
function main(): i32 {
    let x = 2147483647;
    let y = x + 1;
    if (y < 0) { return 1; }
    return 0;
}
`

func TestInterpI32OverflowWraps(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = bytes.NewReader([]byte(i32OverflowWrapProgram))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 1 {
		t.Fatalf("interp exit = %d, want 1 (i32 overflow must wrap to negative)\nstderr: %s", code, errb.String())
	}
}

func TestX86_64I32OverflowWraps(t *testing.T) {
	if _, code := compileAndRunX86_64(t, i32OverflowWrapProgram); code != 1 {
		t.Errorf("x86-64 i32 overflow wrap: exit = %d, want 1", code)
	}
}

func TestArm64I32OverflowWraps(t *testing.T) {
	if _, code := compileAndRunArm64(t, i32OverflowWrapProgram); code != 1 {
		t.Errorf("arm64 i32 overflow wrap: exit = %d, want 1", code)
	}
}

func TestWASMI32OverflowWraps(t *testing.T) {
	if code := runWasm(t, i32OverflowWrapProgram); code != 1 {
		t.Errorf("wasm i32 overflow wrap: exit = %d, want 1", code)
	}
}
