package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const i64MaskFoldSrc = `function main(): i32 {
    let lo: i32 = 0 - 589934592;
    let r: i64 = (lo as i64) & 4294967295;
    return (r / 1000000000) as i32;
}
`

const i64AndNegOneSrc = `function main(): i32 {
    let x: i64 = 5000000000;
    let r: i64 = x & (0 - 1);
    return (r / 1000000000) as i32;
}
`

func TestInterpI64MaskFold(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(i64MaskFoldSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 3 {
		t.Errorf("exit = %d, want 3\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64I64MaskFold(t *testing.T) {
	out, code := compileAndRunX86_64(t, i64MaskFoldSrc)
	if code != 3 {
		t.Errorf("exit = %d, want 3\n%s", code, out)
	}
}

func TestArm64I64MaskFold(t *testing.T) {
	out, code := compileAndRunArm64(t, i64MaskFoldSrc)
	if code != 3 {
		t.Errorf("exit = %d, want 3\n%s", code, out)
	}
}

func TestWASMI64MaskFold(t *testing.T) {
	if code := runWasm(t, i64MaskFoldSrc); code != 3 {
		t.Errorf("wasm exit = %d, want 3", code)
	}
}

func TestX86_64I64AndNegOne(t *testing.T) {
	out, code := compileAndRunX86_64(t, i64AndNegOneSrc)
	if code != 5 {
		t.Errorf("exit = %d, want 5\n%s", code, out)
	}
}

func TestArm64I64AndNegOne(t *testing.T) {
	out, code := compileAndRunArm64(t, i64AndNegOneSrc)
	if code != 5 {
		t.Errorf("exit = %d, want 5\n%s", code, out)
	}
}

const i64LowZeroFoldSrc = `function main(): i32 {
    let f: i64 = 7;
    if ((f | 4503599627370496) / 1000000000000000 != 4) { return 1; }   // 7 | 2^52
    if ((f | 4294967296) / 1000000000 != 4) { return 2; }               // 7 | 2^32
    if ((f | 1099511627776) / 1000000000000 != 1) { return 3; }         // 7 | 2^40
    if ((f ^ 4503599627370496) / 1000000000000000 != 4) { return 4; }   // 7 ^ 2^52
    if ((f + 4294967296) / 1000000000 != 4) { return 5; }               // 7 + 2^32
    let big: i64 = 4294967303;
    if ((big - 4294967296) != 7) { return 6; }                          // big - 2^32
    let m: i64 = 3;
    if ((m * 4294967296) / 1000000000 != 12) { return 7; }              // 3 * 2^32
    return 0;
}
`

func TestInterpI64LowZeroFold(t *testing.T) {
	if code := runInterpExit(t, i64LowZeroFoldSrc); code != 0 {
		t.Errorf("interp exit = %d, want 0", code)
	}
}

func TestX86_64I64LowZeroFold(t *testing.T) {
	out, code := compileAndRunX86_64(t, i64LowZeroFoldSrc)
	if code != 0 {
		t.Errorf("exit = %d, want 0\n%s", code, out)
	}
}

func TestArm64I64LowZeroFold(t *testing.T) {
	out, code := compileAndRunArm64(t, i64LowZeroFoldSrc)
	if code != 0 {
		t.Errorf("exit = %d, want 0\n%s", code, out)
	}
}

func TestWASMI64LowZeroFold(t *testing.T) {
	if code := runWasm(t, i64LowZeroFoldSrc); code != 0 {
		t.Errorf("wasm exit = %d, want 0", code)
	}
}
