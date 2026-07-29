package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A shift masks its count to the operand width — 5 bits at i32, 6 at i64 — so
// a count at or past the width wraps rather than annihilating the value, and a
// negative count wraps to the top of the range. Every layer that evaluates a
// shift has to apply that rule independently, and each of them reached it a
// different way: the constant folder works in Go's int64 (where `1 << 64` is
// 0), x86 masks implicitly to the width of the destination register, arm64 to
// the width of the register the instruction names, wasm by opcode.
//
// This program checks the layers against each other rather than against a
// hardcoded table: CA/CB/CC fold at compile time while lsh/rsh compute the
// same shifts at runtime, so a divergence between the folder and codegen shows
// up as a missing bit. All eight checks pass → 255.
const shiftCountMaskSrc = `const CA: i32 = 1 << 64;
const CB: i32 = 1 << 65;
const CC: i32 = 256 >> 65;

function lsh(x: i32, c: i32): i32 { return x << c; }
function rsh(x: i32, c: i32): i32 { return x >> c; }
function lsh64(x: i64, c: i64): i64 { return x << c; }
function rsh64(x: i64, c: i64): i64 { return x >> c; }

function main(): i32 {
    var agree: i32 = 0;
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
