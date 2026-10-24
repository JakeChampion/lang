package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const tryDynErrorSrc = `trait Error { function message(self: Self): string; }
struct NotFound { what: string }
impl Error for NotFound { function message(self: Self): string { return self.what; } }
function find(ok: boolean): Result[i32, NotFound] {
    if (ok) { return Ok(42); }
    return Err(NotFound { what: "missing" });
}
function handler(ok: boolean): Result[i32, dyn Error] {
    let v: i32 = find(ok)?;
    return Ok(v + 1);
}
function main(): i32 {
    let a: i32 = match (handler(true)) { Ok(v) => v, Err(e) => 0 };
    let b: i32 = match (handler(false)) { Ok(v) => 0, Err(e) => e.message().len() };
    return a + b;
}
`

func TestInterpTryDynError(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(tryDynErrorSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 50 {
		t.Errorf("exit = %d, want 50\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64TryDynError(t *testing.T) {
	if out, code := compileAndRunX86_64(t, tryDynErrorSrc); code != 50 {
		t.Errorf("exit = %d, want 50\n%s", code, out)
	}
}

func TestArm64TryDynError(t *testing.T) {
	if out, code := compileAndRunArm64(t, tryDynErrorSrc); code != 50 {
		t.Errorf("exit = %d, want 50\n%s", code, out)
	}
}

func TestWASMTryDynError(t *testing.T) {
	if code := runWasm(t, tryDynErrorSrc); code != 50 {
		t.Errorf("wasm exit = %d, want 50", code)
	}
}

const tryDynMultiTraitSrc = `trait Code { function code(self: Self): i32; }
trait Msg { function msg(self: Self): i32; }
struct E { c: i32 }
impl Code for E { function code(self: Self): i32 { return self.c; } }
impl Msg for E { function msg(self: Self): i32 { return 100; } }
function find(ok: boolean): Result[i32, E] {
    if (ok) { return Ok(42); }
    return Err(E { c: 7 });
}
function handler(ok: boolean): Result[i32, dyn Code + Msg] {
    let v: i32 = find(ok)?;
    return Ok(v);
}
function main(): i32 {
    let a: i32 = match (handler(true)) { Ok(v) => v, Err(e) => 0 };
    let b: i32 = match (handler(false)) { Ok(v) => 0, Err(e) => e.code() + e.msg() };
    return a + b;
}
`

func TestInterpTryDynMultiTrait(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(tryDynMultiTraitSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 149 {
		t.Errorf("exit = %d, want 149\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64TryDynMultiTrait(t *testing.T) {
	if out, code := compileAndRunX86_64(t, tryDynMultiTraitSrc); code != 149 {
		t.Errorf("exit = %d, want 149\n%s", code, out)
	}
}

func TestArm64TryDynMultiTrait(t *testing.T) {
	if out, code := compileAndRunArm64(t, tryDynMultiTraitSrc); code != 149 {
		t.Errorf("exit = %d, want 149\n%s", code, out)
	}
}

func TestWASMTryDynMultiTrait(t *testing.T) {
	if code := runWasm(t, tryDynMultiTraitSrc); code != 149 {
		t.Errorf("wasm exit = %d, want 149", code)
	}
}

const directDynErrorSrc = `trait Error { function message(self: Self): string; }
struct NotFound { what: string }
impl Error for NotFound { function message(self: Self): string { return self.what; } }
struct Timeout { secs: i32 }
impl Error for Timeout { function message(self: Self): string { return "timeout"; } }
function handler(which: i32): Result[i32, dyn Error] {
    if (which == 0) { return Ok(43); }
    if (which == 1) { return Err(NotFound { what: "missing" }); }
    return Err(Timeout { secs: 5 });
}
function main(): i32 {
    let a: i32 = match (handler(0)) { Ok(v) => v, Err(e) => 0 };
    let b: i32 = match (handler(1)) { Ok(v) => 0, Err(e) => e.message().len() };
    let c: i32 = match (handler(2)) { Ok(v) => 0, Err(e) => e.message().len() };
    return a + b + c;
}
`

func TestInterpDirectDynError(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(directDynErrorSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 57 {
		t.Errorf("exit = %d, want 57\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64DirectDynError(t *testing.T) {
	if out, code := compileAndRunX86_64(t, directDynErrorSrc); code != 57 {
		t.Errorf("exit = %d, want 57\n%s", code, out)
	}
}

func TestArm64DirectDynError(t *testing.T) {
	if out, code := compileAndRunArm64(t, directDynErrorSrc); code != 57 {
		t.Errorf("exit = %d, want 57\n%s", code, out)
	}
}

func TestWASMDirectDynError(t *testing.T) {
	if code := runWasm(t, directDynErrorSrc); code != 57 {
		t.Errorf("wasm exit = %d, want 57", code)
	}
}
