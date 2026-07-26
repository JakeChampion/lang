package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

// The saturating operators `+|` / `-|` / `*|` / `<<|` (#5542) clamp to the operand
// type's [MIN, MAX] instead of wrapping. `+` / `-` / `*` keep their wrapping,
// never-trapping semantics (docs/INTEGER-SEMANTICS.md) — this is purely
// additive surface.
//
// The program returns 0 when every case holds and the index of the first
// failing case otherwise, so a regression names itself in the exit code. Each
// backend runs the identical source; the clamp bounds are baked in at the
// operand width, so i32 / i64 / u8 / u32 / u64 are all exercised.
//
// `i32min` / `i64min` are spelled `0 - MAX - 1` because a bare `-2147483648`
// literal is rejected (E047: the magnitude is checked before the unary minus).
const saturatingArithProgram = `
function main(): i32 {
    var i32min: i32 = 0 - 2147483647 - 1;
    var a: i32 = 2147483647;
    var b: i32 = 1;
    if ((a +| b) != 2147483647) { return 1; }
    if ((i32min -| b) != i32min) { return 2; }
    if ((a +| (0 - 1)) != 2147483646) { return 3; }
    if ((100000 *| 100000) != 2147483647) { return 4; }
    if (((0 - 100000) *| 100000) != i32min) { return 5; }
    if ((3 *| 4) != 12) { return 6; }
    if ((10 -| 3) != 7) { return 7; }
    if ((i32min *| (0 - 1)) != 2147483647) { return 8; }
    if (((0 - 1) *| i32min) != 2147483647) { return 9; }
    if ((0 *| i32min) != 0) { return 10; }
    if ((i32min +| i32min) != i32min) { return 11; }

    var u: u8 = 250;
    var v: u8 = 10;
    if ((u +| v) != 255) { return 20; }
    if ((v -| u) != 0) { return 21; }
    if ((u *| v) != 255) { return 22; }
    if ((v *| 2) != 20) { return 23; }
    if ((u -| v) != 240) { return 24; }

    var p: u32 = 4294967290;
    var q: u32 = 10;
    if ((p +| q) != 4294967295) { return 30; }
    if ((q -| p) != 0) { return 31; }
    if ((p *| q) != 4294967295) { return 32; }
    if ((q *| q) != 100) { return 33; }

    var i64min: i64 = 0 - 9223372036854775807 - 1;
    var x: i64 = 9223372036854775807;
    var y: i64 = 5;
    if ((x +| y) != 9223372036854775807) { return 40; }
    if ((x *| y) != 9223372036854775807) { return 41; }
    if ((i64min -| y) != i64min) { return 42; }
    if ((i64min *| (0 - 1)) != 9223372036854775807) { return 43; }
    if ((x *| 0) != 0) { return 44; }
    if ((y *| y) != 25) { return 45; }
    if ((x -| x) != 0) { return 46; }

    var m: u64 = 18446744073709551615;
    if ((m +| 1) != 18446744073709551615) { return 50; }
    if ((m *| 2) != 18446744073709551615) { return 51; }
    if ((m -| m) != 0) { return 52; }

    // Saturating shift-left. Unlike the other three it post-checks by
    // shifting back: the negative-side pre-check bound would need a ceiling
    // division that an arithmetic shift cannot express.
    if ((1 <<| 30) != 1073741824) { return 70; }
    if ((1 <<| 31) != 2147483647) { return 71; }
    if ((2 <<| 30) != 2147483647) { return 72; }
    if (((0 - 1) <<| 31) != i32min) { return 73; }
    if (((0 - 2) <<| 31) != i32min) { return 74; }
    if ((0 <<| 31) != 0) { return 75; }
    if (((0 - 1) <<| 0) != (0 - 1)) { return 76; }
    if (((0 - 1073741824) <<| 1) != i32min) { return 77; }
    if ((1073741824 <<| 1) != 2147483647) { return 78; }
    // The count is masked exactly as the wrapping shift masks it, so a
    // count past the width wraps to a small one rather than saturating.
    if ((3 <<| 32) != 3) { return 79; }
    // u8 wraps into its 8-bit slot, so the round-trip has to see the
    // wrapped value: 200u8 << 1 is 144, which shifts back to 72, not 200.
    if ((u <<| v) != 255) { return 80; }
    if ((v <<| (1 as u8)) != 20) { return 81; }
    if (((100 as u8) <<| (1 as u8)) != 200) { return 82; }
    if (((200 as u8) <<| (1 as u8)) != 255) { return 83; }
    if ((p <<| (1 as u32)) != 4294967295) { return 84; }
    if (((1 as u32) <<| (31 as u32)) != 2147483648) { return 85; }
    if ((q <<| (1 as u32)) != 20) { return 86; }
    if ((x <<| (1 as i64)) != 9223372036854775807) { return 87; }
    if (((1 as i64) <<| (62 as i64)) != 4611686018427387904) { return 88; }
    if (((1 as i64) <<| (63 as i64)) != 9223372036854775807) { return 89; }
    if (((0 - 1) as i64 <<| (63 as i64)) != i64min) { return 90; }
    if ((m <<| (1 as u64)) != 18446744073709551615) { return 91; }
    if (((1 as u64) <<| (63 as u64)) != 9223372036854775808) { return 92; }

    // The wrapping operators are unchanged.
    if ((a + b) != i32min) { return 60; }
    if ((u + v) != 4) { return 61; }
    if ((1 << 31) != i32min) { return 62; }
    return 0;
}
`

// saturatingPrecedenceProgram pins the precedence tier of each new operator:
// `+|` / `-|` bind like `+` / `-`, `*|` binds like `*`.
const saturatingPrecedenceProgram = `
function main(): i32 {
    // 2 +| 3 * 4 == 2 +| 12 == 14 -- multiplication binds tighter.
    if ((2 +| 3 * 4) != 14) { return 1; }
    // 2 + 3 *| 4 == 2 + 12 == 14 -- saturating mul binds tighter.
    if ((2 + 3 *| 4) != 14) { return 2; }
    // Left-associative within the tier: (10 -| 3) -| 4 == 3.
    if ((10 -| 3 -| 4) != 3) { return 3; }
    // Shift is looser than the additive tier: 1 << 2 +| 1 == 1 << 3 == 8.
    if ((1 << 2 +| 1) != 8) { return 4; }
    // Saturating shift sits in the same tier as the wrapping one:
    // 1 <<| 2 + 1 == 1 <<| 3 == 8.
    if ((1 <<| 2 + 1) != 8) { return 5; }
    // Left-associative within the shift tier: (1 <<| 2) <<| 3 == 32.
    if ((1 <<| 2 <<| 3) != 32) { return 6; }
    // ... and looser than the comparison it feeds is tighter than: the
    // shift happens first, so this is (1 <<| 3) > 4.
    if (!(1 <<| 3 > 4)) { return 7; }
    return 0;
}
`

func TestInterpSaturatingArith(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"ops", saturatingArithProgram},
		{"precedence", saturatingPrecedenceProgram},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := buildLangBinForInterp(t)
			cmd := exec.Command(bin, "-interp", "-")
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			var out, errb bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &errb
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Fatalf("interp exit = %d, want 0 (case %d failed)\nstderr: %s", code, code, errb.String())
			}
		})
	}
}

func TestX86_64SaturatingArith(t *testing.T) {
	if _, code := compileAndRunX86_64(t, saturatingArithProgram); code != 0 {
		t.Errorf("x86-64 saturating arith: exit = %d, want 0 (case %d failed)", code, code)
	}
	if _, code := compileAndRunX86_64(t, saturatingPrecedenceProgram); code != 0 {
		t.Errorf("x86-64 saturating precedence: exit = %d, want 0 (case %d failed)", code, code)
	}
}

func TestArm64SaturatingArith(t *testing.T) {
	if _, code := compileAndRunArm64(t, saturatingArithProgram); code != 0 {
		t.Errorf("arm64 saturating arith: exit = %d, want 0 (case %d failed)", code, code)
	}
	if _, code := compileAndRunArm64(t, saturatingPrecedenceProgram); code != 0 {
		t.Errorf("arm64 saturating precedence: exit = %d, want 0 (case %d failed)", code, code)
	}
}

func TestWASMSaturatingArith(t *testing.T) {
	if code := runWasm(t, saturatingArithProgram); code != 0 {
		t.Errorf("wasm saturating arith: exit = %d, want 0 (case %d failed)", code, code)
	}
	if code := runWasm(t, saturatingPrecedenceProgram); code != 0 {
		t.Errorf("wasm saturating precedence: exit = %d, want 0 (case %d failed)", code, code)
	}
}
