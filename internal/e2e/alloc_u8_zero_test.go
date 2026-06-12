package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

// allocU8ZeroProgram exercises the __alloc_u8 zero-initialisation contract
// (issue #2768). fill() allocates a u8[], writes 0xAB into every byte, and
// returns — dropping (freeing) the buffer so it lands on the allocator's
// freelist. check() then allocates the SAME size, which reuses that freed
// block, and sums its bytes: 0 iff the reused block was zero-filled, or
// 32*0xAB otherwise. The interpreter always zeroed (Go make); the AOT backends
// did not until #2768, so a reused block carried stale bytes. main returns the
// sum, so exit 0 means correctly zero-initialised on every backend.
const allocU8ZeroProgram = `
function fill(): i32 {
    var a: u8[] = __alloc_u8(32);
    var i: i32 = 0;
    while (i < 32) { a = a.with(i, 0xAB as u8); i = i + 1; }
    return a[0] as i32;
}
function check(): i32 {
    var b: u8[] = __alloc_u8(32);
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < 32) { s = s + (b[i] as i32); i = i + 1; }
    return s;
}
function main(): i32 { var x: i32 = fill(); return check(); }
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
