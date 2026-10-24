package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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

const runtimeHelperNameFnSrc = `function __fern_alloc(x: i32): i32 { return x + 1; }
function main(): i32 {
    let a: i32[] = [1, 2, 3];
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

const loweringHelperNameFnSrc = `function __fern_str_append(x: i32): i32 { return x + 1; }
function main(): i32 {
    let s: string = "a";
    for i in 0..3 { s = s + "b"; }
    return __fern_str_append(s.len() + 37);
}
`

func TestInterpLoweringHelperNameFn(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(loweringHelperNameFnSrc), 0o644); err != nil {
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

func TestX86_64LoweringHelperNameFn(t *testing.T) {
	out, code := compileAndRunX86_64(t, loweringHelperNameFnSrc)
	if code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestArm64LoweringHelperNameFn(t *testing.T) {
	out, code := compileAndRunArm64(t, loweringHelperNameFnSrc)
	if code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestWASMLoweringHelperNameFn(t *testing.T) {
	if code := runWasm(t, loweringHelperNameFnSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}
