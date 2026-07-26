package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

// The checked operators `+?` / `-?` / `*?` (#5542) evaluate to `Some(result)`
// when the exact result fits the operand type and `None` on overflow. They are
// additive surface: `+` / `-` / `*` keep their wrapping, never-trapping
// semantics (docs/INTEGER-SEMANTICS.md). The operators desugar to an
// `Option`-yielding block-expr (Binary.CheckedLowered), so every backend lowers
// them through the ordinary Option / if / wrapping-arithmetic paths.
//
// The program returns 0 when every case holds and the index of the first
// failing case otherwise, so a regression names itself in the exit code. Each
// backend runs the identical source; the overflow bound is baked in at the
// operand width, so i32 / i64 / u8 / u32 / u64 are all exercised. A `None`
// where `Some` was expected (or vice versa) is a distinct failure code from a
// wrong `Some` payload.
//
// `i32min` / `i64min` are spelled `0 - MAX - 1` because a bare `-2147483648`
// literal is rejected (E047: the magnitude is checked before the unary minus).
const checkedArithProgram = `
function main(): i32 {
    var i32min: i32 = 0 - 2147483647 - 1;
    var i32max: i32 = 2147483647;
    // i32 signed add: overflow high / underflow low / passthrough.
    match (i32max +? 1)     { Some(v) => { return 1; }, None => {} }
    match (i32min +? (0-1)) { Some(v) => { return 2; }, None => {} }
    match (40 +? 2)         { Some(v) => { if (v != 42) { return 3; } }, None => { return 4; } }
    // i32 signed sub.
    match (i32min -? 1)     { Some(v) => { return 5; }, None => {} }
    match (10 -? 3)         { Some(v) => { if (v != 7) { return 6; } }, None => { return 7; } }
    // i32 signed mul, incl. the (-1, MIN) division-round-trip edge.
    match (100000 *? 100000){ Some(v) => { return 8; }, None => {} }
    match (i32min *? (0-1)) { Some(v) => { return 9; }, None => {} }
    match ((0-1) *? i32min) { Some(v) => { return 10; }, None => {} }
    match (0 *? i32min)     { Some(v) => { if (v != 0) { return 11; } }, None => { return 12; } }
    match (6 *? 7)          { Some(v) => { if (v != 42) { return 13; } }, None => { return 14; } }

    // u8: clamp low is 0, saturated result in range by construction.
    var u: u8 = 250; var w: u8 = 10;
    match (u +? w)          { Some(v) => { return 20; }, None => {} }
    match (w -? u)          { Some(v) => { return 21; }, None => {} }
    match (u *? w)          { Some(v) => { return 22; }, None => {} }
    match (u -? w)          { Some(v) => { if ((v as i32) != 240) { return 23; } }, None => { return 24; } }

    // u32.
    var p: u32 = 4294967290; var q: u32 = 10;
    match (p +? q)          { Some(v) => { return 30; }, None => {} }
    match (q -? p)          { Some(v) => { return 31; }, None => {} }
    match (p *? q)          { Some(v) => { return 32; }, None => {} }
    match (q *? q)          { Some(v) => { if ((v as i64) != 100) { return 33; } }, None => { return 34; } }

    // i64 at full width — no wider host type.
    var x: i64 = 9223372036854775807; var i64min: i64 = 0 - 9223372036854775807 - 1;
    match (x +? 5)          { Some(v) => { return 40; }, None => {} }
    match (i64min -? 5)     { Some(v) => { return 41; }, None => {} }
    match (x *? 5)          { Some(v) => { return 42; }, None => {} }
    match (i64min *? (0-1)) { Some(v) => { return 43; }, None => {} }
    match ((0-1) *? i64min) { Some(v) => { return 44; }, None => {} }
    match (x *? 0)          { Some(v) => { if ((v as i32) != 0) { return 45; } }, None => { return 46; } }
    match ((6 as i64) *? 7) { Some(v) => { if ((v as i32) != 42) { return 47; } }, None => { return 48; } }

    // u64.
    var m: u64 = 18446744073709551615;
    match (m +? 1)          { Some(v) => { return 50; }, None => {} }
    match ((0 as u64) -? 1) { Some(v) => { return 51; }, None => {} }
    match (m *? 2)          { Some(v) => { return 52; }, None => {} }
    match ((21 as u64) +? 21) { Some(v) => { if ((v as i32) != 42) { return 53; } }, None => { return 54; } }

    // The wrapping operators are unchanged.
    if ((i32max + 1) != i32min) { return 60; }
    return 0;
}
`

func TestInterpCheckedArith(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = bytes.NewReader([]byte(checkedArithProgram))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("interp exit = %d, want 0 (case %d failed)\nstderr: %s", code, code, errb.String())
	}
}

func TestX86_64CheckedArith(t *testing.T) {
	if _, code := compileAndRunX86_64(t, checkedArithProgram); code != 0 {
		t.Errorf("x86-64 checked arith: exit = %d, want 0 (case %d failed)", code, code)
	}
}

func TestArm64CheckedArith(t *testing.T) {
	if _, code := compileAndRunArm64(t, checkedArithProgram); code != 0 {
		t.Errorf("arm64 checked arith: exit = %d, want 0 (case %d failed)", code, code)
	}
}

func TestWASMCheckedArith(t *testing.T) {
	if code := runWasm(t, checkedArithProgram); code != 0 {
		t.Errorf("wasm checked arith: exit = %d, want 0 (case %d failed)", code, code)
	}
}
