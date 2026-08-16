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

// The escape above covered only the registers the backend itself emits, so
// every other token GNU `as` reserves in Intel syntax still reached the
// assembler bare (#6022) — and only on the `-cc` path, since this project's
// own assembler knows none of them and reads them as the symbols they are.
// One name per class that was missing: a segment register (the issue's
// reproducer, `.size cs, .-cs` does not evaluate), an APX extended GPR
// (`call r16` assembles clean as a REX2 indirect call — no diagnostic, then
// SIGSEGV), MMX / control / instruction-pointer registers, an Intel-syntax
// expression operator and a size keyword.
//
// The x86-64 leg is the one that regresses; the other three run it because a
// reserved asm token must not become a frontend problem either.
//
// cs(1)=2, gs(1)=3, r16(1)=4, mm0(1)=5, cr0(1)=6, rip(1)=7, mod(1)=8,
// qword(1)=9 → 2+3+4+5+6+7+8+9 = 44.
const asmReservedNameFnSrc = `function cs(x: i32): i32 { return x + 1; }
function gs(x: i32): i32 { return x + 2; }
function r16(x: i32): i32 { return x + 3; }
function mm0(x: i32): i32 { return x + 4; }
function cr0(x: i32): i32 { return x + 5; }
function rip(x: i32): i32 { return x + 6; }
function mod(x: i32): i32 { return x + 7; }
function qword(x: i32): i32 { return x + 8; }
function main(): i32 {
    return cs(1) + gs(1) + r16(1) + mm0(1) + cr0(1) + rip(1) + mod(1) + qword(1);
}
`

func TestInterpAsmReservedNameFn(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(asmReservedNameFnSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 44 {
		t.Errorf("exit = %d, want 44\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

// compileAndRunX86_64 assembles and links with gcc, which is the path #6022
// broke: before the fix this failed to build at all
// (`Error: .size expression for cs does not evaluate to a constant`).
func TestX86_64AsmReservedNameFn(t *testing.T) {
	out, code := compileAndRunX86_64(t, asmReservedNameFnSrc)
	if code != 44 {
		t.Errorf("exit = %d, want 44\n%s", code, out)
	}
}

func TestArm64AsmReservedNameFn(t *testing.T) {
	out, code := compileAndRunArm64(t, asmReservedNameFnSrc)
	if code != 44 {
		t.Errorf("exit = %d, want 44\n%s", code, out)
	}
}

func TestWASMAsmReservedNameFn(t *testing.T) {
	if code := runWasm(t, asmReservedNameFnSrc); code != 44 {
		t.Errorf("wasm exit = %d, want 44", code)
	}
}
