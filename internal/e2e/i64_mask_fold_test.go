package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Regression: the AST identity fold `x & -1 → x` (maybeFoldArithIdentity)
// keyed on constNumber, which truncates to int32 — so a 64-bit mask literal
// 4294967295 (0xFFFFFFFF) also read as -1 and the AND was silently dropped.
// That is wrong for an i64 operand: `lo as i64` sign-extends a negative i32,
// so `& 0xFFFFFFFF` must CLEAR the high 32 bits, not be a no-op. Here lo =
// -589934592 sign-extends to 0xFFFFFFFF_DCD65000; masking yields 0xDCD65000
// (3705032704), and /1e9 == 3. Without the mask the value stays negative and
// /1e9 == 0. The fold is now restricted to non-64-bit widths.
const i64MaskFoldSrc = `function main(): i32 {
    var lo: i32 = 0 - 589934592;
    var r: i64 = (lo as i64) & 4294967295;
    return (r / 1000000000) as i32;
}
`

// A genuine i64 `x & -1` (all 64 bits set) must still be identity — the fix
// keeps the (harmless) AND rather than folding, and the result is unchanged.
const i64AndNegOneSrc = `function main(): i32 {
    var x: i64 = 5000000000;
    var r: i64 = x & (0 - 1);
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

// Regression (#5567): the other AST identity folds in maybeFoldArithIdentity
// (`x | 0`, `x ^ 0`, `x + 0`, `x - 0`, `x * 2^k`) also keyed on constNumber,
// which truncated the literal to int32. So any i64 literal whose low 32 bits
// are zero — 2^32, 2^40, 2^52, … — read as 0 (or a truncated power of two)
// and the operation was silently dropped or mis-shifted. `7 | 2^52` folded to
// `7` on every native backend while the interp (which reads the full Value)
// stayed correct. The fix makes constNumber return the full int64. Each arm
// returns a distinct nonzero code so a failure names the broken fold.
const i64LowZeroFoldSrc = `function main(): i32 {
    var f: i64 = 7;
    if ((f | 4503599627370496) / 1000000000000000 != 4) { return 1; }   // 7 | 2^52
    if ((f | 4294967296) / 1000000000 != 4) { return 2; }               // 7 | 2^32
    if ((f | 1099511627776) / 1000000000000 != 1) { return 3; }         // 7 | 2^40
    if ((f ^ 4503599627370496) / 1000000000000000 != 4) { return 4; }   // 7 ^ 2^52
    if ((f + 4294967296) / 1000000000 != 4) { return 5; }               // 7 + 2^32
    var big: i64 = 4294967303;
    if ((big - 4294967296) != 7) { return 6; }                          // big - 2^32
    var m: i64 = 3;
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
