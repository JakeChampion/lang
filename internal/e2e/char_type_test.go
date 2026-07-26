package e2e

import "testing"

// charTypeProgram exercises the `char` Unicode-scalar type end-to-end
// (#5629 slice 1): a `char` var, param and return; explicit conversion in
// both directions (`n as char`, `c as i32`); `char[]` arrays; a scalar
// beyond the BMP; and char-to-char assignment and equality.
//
// In this first slice no stdlib producer yields `char` — the type exists so
// signatures can declare scalar-vs-byte intent, mirroring how `str` landed.
// CharType is erased to i32 at the LowerWith choke point
// (ir/erase_surface.go), so a correct run proves the erasure feeds every
// backend a plain i32 program. Exits 0 on success, a distinct code per
// failed step.
const charTypeProgram = `
function scalar_of(c: char): i32 { return c as i32; }

function as_char(n: i32): char { return n as char; }

function upper_ascii(c: char): char {
    var n: i32 = c as i32;
    if (n >= 97 && n <= 122) { return (n - 32) as char; }
    return c;
}

function main(): i32 {
    var c: char = 97 as char;
    if (scalar_of(c) != 97) { return 1; }
    // char-to-char assignment (Equal, no conversion).
    var d: char = c;
    if (scalar_of(d) != 97) { return 2; }
    // Round trip through the integer and back.
    if (scalar_of(as_char(scalar_of(c))) != 97) { return 3; }
    // A scalar outside the BMP still rides the i32 slot intact.
    if (scalar_of(as_char(128512)) != 128512) { return 4; }
    if (scalar_of(as_char(1114111)) != 1114111) { return 5; }
    // char[] arrays: element type survives, indexes as a char.
    var arr: char[] = [65 as char, 66 as char, 67 as char];
    if (scalar_of(arr[2]) != 67) { return 6; }
    var pick: char = arr[0];
    if (scalar_of(pick) != 65) { return 7; }
    // A char-taking, char-returning function composes.
    if (scalar_of(upper_ascii(c)) != 65) { return 8; }
    if (scalar_of(upper_ascii(as_char(48))) != 48) { return 9; }
    // Equality between chars.
    if (!(c == d)) { return 10; }
    if (c == as_char(98)) { return 11; }
    // The cast is an expression, usable inline in arithmetic.
    if ((c as i32) + 1 != 98) { return 12; }
    return 0;
}
`

func TestCharTypeInterp(t *testing.T) {
	if got := runInterpExit(t, charTypeProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestCharTypeX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, charTypeProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestCharTypeWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, charTypeProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestCharTypeArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, charTypeProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
