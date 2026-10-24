package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

const allocU8ZeroProgram = `
function fill(): i32 {
    let a: u8[] = __alloc_u8(32);
    let i: i32 = 0;
    while (i < 32) { a = a.with(i, 0xAB as u8); i = i + 1; }
    return a[0] as i32;
}
function check(): i32 {
    let b: u8[] = __alloc_u8(32);
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < 32) { s = s + (b[i] as i32); i = i + 1; }
    return s;
}
function main(): i32 { let x: i32 = fill(); return check(); }
`

func TestInterpAllocU8Zero(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = bytes.NewReader([]byte(allocU8ZeroProgram))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 (reused u8[] not zero-filled)\nstderr: %s", code, errb.String())
	}
}

func TestX86_64AllocU8Zero(t *testing.T) {
	if _, code := compileAndRunX86_64(t, allocU8ZeroProgram); code != 0 {
		t.Errorf("x86-64 __alloc_u8 zero-init: exit = %d, want 0", code)
	}
}

func TestArm64AllocU8Zero(t *testing.T) {
	if _, code := compileAndRunArm64(t, allocU8ZeroProgram); code != 0 {
		t.Errorf("arm64 __alloc_u8 zero-init: exit = %d, want 0", code)
	}
}

func TestWASMAllocU8Zero(t *testing.T) {
	if code := runWasm(t, allocU8ZeroProgram); code != 0 {
		t.Errorf("wasm __alloc_u8 zero-init: exit = %d, want 0", code)
	}
}
