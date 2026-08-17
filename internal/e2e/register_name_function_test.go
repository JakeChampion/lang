package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A user function whose name is an x86 register mnemonic (`ch`, `al`, `si`,
// `ax`, …) must compile and run correctly. The native backends mangle every
// Fern function symbol, so no assembler ever sees the bare token: without
// that, `call ch` resolves to the register CH and encodes an indirect call
// through garbage → SIGSEGV. This exercises several such names, called and
// returning, so a regression re-segfaults here on x86-64 (and stays correct on
// the other backends, which were never affected).
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

// One name per class GNU `as` resolves as something other than a symbol, all
// of which used to reach the assembler bare (#6022) — and only on the `-cc`
// path, since this project's own assembler knows none of them and reads them
// as the symbols they are: a segment register (the issue's reproducer,
// `.size cs, .-cs` does not evaluate), an APX extended GPR (`call r16`
// assembles clean as a REX2 indirect call — no diagnostic, then SIGSEGV),
// MMX / control / instruction-pointer registers, an Intel-syntax expression
// operator and a size keyword.
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
// (`Error: .size expression for cs does not evaluate to a constant`) while the
// in-process assembler built the same source happily.
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

// The other half of the same namespace: the runtime helpers the backends emit
// alongside the program. A Fern function named after one used to define that
// symbol twice — gcc said `symbol '__fern_alloc' is already defined`, and the
// in-process assembler (the DEFAULT for `-target x86-64`) took the first
// definition and built a binary that called the runtime helper wherever the
// program meant its own function. Silently: the program below returned 32
// instead of 42. Mangling separates the two namespaces, so the helper keeps
// its name and the Fern function gets its own.
//
// The array forces __fern_alloc to actually be emitted; a program that never
// allocates has no helper to collide with.
//
// __fern_alloc(3 + 38) = 42.
const runtimeHelperNameFnSrc = `function __fern_alloc(x: i32): i32 { return x + 1; }
function main(): i32 {
    var a: i32[] = [1, 2, 3];
    return __fern_alloc(a.len() + 38);
}
`

func TestInterpRuntimeHelperNameFn(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(runtimeHelperNameFnSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("exit = %d, want 42\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64RuntimeHelperNameFn(t *testing.T) {
	out, code := compileAndRunX86_64(t, runtimeHelperNameFnSrc)
	if code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestArm64RuntimeHelperNameFn(t *testing.T) {
	out, code := compileAndRunArm64(t, runtimeHelperNameFnSrc)
	if code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestWASMRuntimeHelperNameFn(t *testing.T) {
	if code := runWasm(t, runtimeHelperNameFnSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}
