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

// charUnicodeProgram pins std/unicode's `char` surface (#5629 slice 5) —
// the first stdlib producer/consumer of the type.
//
// These are METHODS, not free functions, and that is load-bearing: a free
// `to_upper(c: char)` would collide with `to_upper(s: string)` in the same
// module. The method form also makes the receiver TYPE carry the meaning,
// which is the whole point of D2 — `c.to_upper()` and `s.to_upper()` are
// visibly different operations where previously both sides were `i32` and
// only a naming convention separated them.
//
// Case mapping on a `char` is SIMPLE: a 1→N expansion has no single scalar
// to return, so `ß` maps to itself here while `"ß".to_upper()` is `"SS"`.
// Step 12 pins exactly that contrast.
const charUnicodeProgram = `
import "std/string";
import "std/unicode" as unicode;

function up(n: i32): i32 { return (n as char).to_upper() as i32; }
function lo(n: i32): i32 { return (n as char).to_lower() as i32; }

function main(): i32 {
    // Simple case mapping over scalars, ASCII and beyond.
    if (up(97) != 65) { return 1; }
    if (lo(65) != 97) { return 2; }
    if (lo(913) != 945) { return 3; }        // Greek capital alpha
    if (up(0xB5) != 0x39C) { return 4; }     // MICRO SIGN → Greek capital mu
    if (up(0x10428) != 0x10400) { return 5; } // Deseret, beyond the BMP
    if (up(48) != 48) { return 6; }          // a digit is caseless

    // Classification.
    var a: char = 97 as char;
    if (!a.is_letter() || !a.is_lower() || a.is_upper() || !a.is_alnum()) { return 7; }
    var zero: char = 48 as char;
    if (!zero.is_digit() || !zero.is_alnum() || zero.is_letter()) { return 8; }
    // Nd is the DECIMAL class, so Arabic-Indic digits count.
    if (!((0x0669 as char).is_digit())) { return 9; }
    if (!((0xA0 as char).is_whitespace())) { return 10; }
    if ((97 as char).is_whitespace()) { return 11; }

    // char case mapping is SIMPLE; the string one is FULL. Same input,
    // deliberately different answers.
    if (up(223) != 223) { return 12; }
    if ("ß".to_upper() != "SS") { return 13; }
    return 0;
}
`

func TestCharUnicodeInterp(t *testing.T) {
	if got := runInterpExit(t, charUnicodeProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestCharUnicodeX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, charUnicodeProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestCharUnicodeWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, charUnicodeProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestCharUnicodeArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, charUnicodeProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
