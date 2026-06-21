package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

// i32OverflowWrapProgram pins i32 signed-overflow semantics (#3581): an i32
// add that overflows MUST wrap at 32 bits (i32 is a true 32-bit type, like the
// existing u32/u8/u16 wrap), so 2147483647 + 1 wraps to -2147483648 and the
// `< 0` check is true → exit 1. The compiled backends always wrapped (the value
// lives in a 32-bit register); the AST interpreter is width-driven and used to
// keep the full 64-bit sum (2147483648 > 0 → exit 0) because an unannotated
// `var x = …; var y = x + 1` left the binary's IntWidth unpinned. The checker
// now defaults a leftover-polymorphic integer op to i32, so every path agrees.
const i32OverflowWrapProgram = `
function main(): i32 {
    var x = 2147483647;
    var y = x + 1;
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
