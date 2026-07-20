package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A user function whose name is an x86 register mnemonic (`ch`, `al`, `si`,
// `ax`, …) must compile and run correctly. On x86-64 the native assembler
// used to resolve the `call ch` target to the register CH and emit an
// indirect `call rbp` through garbage → SIGSEGV; the fix reinterprets a
// sub-64-bit register operand in a call/jmp as the colliding symbol. This
// exercises several such names, called and returning, so a regression
// re-segfaults here on x86-64 (and stays correct on the other backends,
// which were never affected).
//
// ch(10)=11, al(20)=19, si(3)=9, ax(4)=5, cx(100)=50 → 11+19+9+5+50 = 94.
const registerNameFnSrc = `function ch(x: i32): i32 { return x + 1; }
function al(x: i32): i32 { return x - 1; }
function si(x: i32): i32 { return x * 3; }
function ax(x: i32): i32 { return x + 1; }
function cx(x: i32): i32 { return x / 2; }
function main(): i32 {
    return ch(10) + al(20) + si(3) + ax(4) + cx(100);
}
`

func TestInterpRegisterNameFn(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(registerNameFnSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 94 {
		t.Errorf("exit = %d, want 94\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64RegisterNameFn(t *testing.T) {
	out, code := compileAndRunX86_64(t, registerNameFnSrc)
	if code != 94 {
		t.Errorf("exit = %d, want 94\n%s", code, out)
	}
}

func TestArm64RegisterNameFn(t *testing.T) {
	out, code := compileAndRunArm64(t, registerNameFnSrc)
	if code != 94 {
		t.Errorf("exit = %d, want 94\n%s", code, out)
	}
}

func TestWASMRegisterNameFn(t *testing.T) {
	if code := runWasm(t, registerNameFnSrc); code != 94 {
		t.Errorf("wasm exit = %d, want 94", code)
	}
}
