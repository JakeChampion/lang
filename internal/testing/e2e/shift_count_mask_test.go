package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const shiftCountMaskSrc = `const CA: i32 = 1 << 64;
const CB: i32 = 1 << 65;
const CC: i32 = 256 >> 65;

function lsh(x: i32, c: i32): i32 { return x << c; }
function rsh(x: i32, c: i32): i32 { return x >> c; }
function lsh64(x: i64, c: i64): i64 { return x << c; }
function rsh64(x: i64, c: i64): i64 { return x >> c; }

function main(): i32 {
    let agree: i32 = 0;
    if (lsh(1, 64) == CA) { agree = agree + 1; }
    if (lsh(1, 65) == CB) { agree = agree + 2; }
    if (rsh(256, 65) == CC) { agree = agree + 4; }
    if (lsh(460, 124) == (0 - 1073741824)) { agree = agree + 8; }
    if (rsh(0 - 8, 33) == (0 - 4)) { agree = agree + 16; }
    if (lsh(1, 0 - 1) == ((0 - 2147483647) - 1)) { agree = agree + 32; }
    if (lsh64(1i64, 64i64) == 1i64) { agree = agree + 64; }
    if (rsh64(1099511627776i64, 33i64) == 128i64) { agree = agree + 128; }
    return agree;
}
`

func TestInterpShiftCountMask(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(shiftCountMaskSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 255 {
		t.Errorf("exit = %d, want 255\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64ShiftCountMask(t *testing.T) {
	out, code := compileAndRunX86_64(t, shiftCountMaskSrc)
	if code != 255 {
		t.Errorf("exit = %d, want 255\n%s", code, out)
	}
}

func TestArm64ShiftCountMask(t *testing.T) {
	out, code := compileAndRunArm64(t, shiftCountMaskSrc)
	if code != 255 {
		t.Errorf("exit = %d, want 255\n%s", code, out)
	}
}

func TestWASMShiftCountMask(t *testing.T) {
	if code := runWasm(t, shiftCountMaskSrc); code != 255 {
		t.Errorf("wasm exit = %d, want 255", code)
	}
}
