package e2e

import "testing"

// charBoundaryProgram pins std/utf8's byte-boundary layer — the
// is_char_boundary / floor_char_boundary / ceil_char_boundary trio
// (#5634, decision D9, slice 1) — on every backend.
//
// These are the primitives a caller needs to snap a byte offset to a
// safe slice endpoint without hand-rolling the continuation-byte check.
// The test string is A + e-acute + euro + grinning-face: 10 bytes, four
// characters of width 1/2/3/4, so its boundary set is exactly
// {0, 1, 3, 6, 10} and every other index in range sits inside an
// encoding.
//
// Exits 0 on success, a distinct code per failed step.
const charBoundaryProgram = `
import "std/utf8" as utf8;

// A(1) + e-acute(2) + euro(3) + grinning face(4) = 10 bytes.
function mixed(): string {
    var b: u8[] = [65 as u8, 195 as u8, 169 as u8, 226 as u8, 130 as u8,
        172 as u8, 240 as u8, 159 as u8, 152 as u8, 128 as u8];
    return string_from_bytes(b);
}

function main(): i32 {
    var s: string = mixed();
    if (s.len() != 10) { return 1; }

    // The boundary set is exactly {0, 1, 3, 6, 10}.
    var i: i32 = 0;
    while (i <= 10) {
        var want: boolean = i == 0 || i == 1 || i == 3 || i == 6 || i == 10;
        if (utf8.is_char_boundary(s, i) != want) { return 2; }
        i = i + 1;
    }
    // Out of range in either direction is never a boundary.
    if (utf8.is_char_boundary(s, 0 - 1)) { return 3; }
    if (utf8.is_char_boundary(s, 11)) { return 4; }
    // The empty string has exactly one boundary; ASCII has all of them.
    if (!utf8.is_char_boundary("", 0)) { return 5; }
    if (utf8.is_char_boundary("", 1)) { return 6; }
    if (!utf8.is_char_boundary("abc", 2)) { return 7; }

    // floor snaps back to the start of the character it lands inside.
    if (utf8.floor_char_boundary(s, 2) != 1) { return 8; }
    if (utf8.floor_char_boundary(s, 5) != 3) { return 9; }
    if (utf8.floor_char_boundary(s, 9) != 6) { return 10; }
    if (utf8.floor_char_boundary(s, 6) != 6) { return 11; }
    // ceil advances to the end of it.
    if (utf8.ceil_char_boundary(s, 2) != 3) { return 12; }
    if (utf8.ceil_char_boundary(s, 4) != 6) { return 13; }
    if (utf8.ceil_char_boundary(s, 7) != 10) { return 14; }
    if (utf8.ceil_char_boundary(s, 6) != 6) { return 15; }
    // Both clamp at both ends.
    if (utf8.floor_char_boundary(s, 0 - 4) != 0) { return 16; }
    if (utf8.floor_char_boundary(s, 99) != 10) { return 17; }
    if (utf8.ceil_char_boundary(s, 0 - 4) != 0) { return 18; }
    if (utf8.ceil_char_boundary(s, 99) != 10) { return 19; }

    // The property that makes them safe to slice with, over every
    // index pair: a snapped result is a boundary, floor never grows
    // the index, ceil never shrinks it, and the snapped slice is
    // always valid UTF-8.
    var a: i32 = 0;
    while (a <= 10) {
        var lo: i32 = utf8.floor_char_boundary(s, a);
        var hi: i32 = utf8.ceil_char_boundary(s, a);
        if (!utf8.is_char_boundary(s, lo)) { return 20; }
        if (!utf8.is_char_boundary(s, hi)) { return 21; }
        if (lo > a) { return 22; }
        if (hi < a) { return 23; }
        var b: i32 = a;
        while (b <= 10) {
            var cut: str = s[utf8.floor_char_boundary(s, a) : utf8.ceil_char_boundary(s, b)];
            if (!utf8.is_valid_utf8(cut.to_owned())) { return 24; }
            b = b + 1;
        }
        a = a + 1;
    }

    // The unsnapped slice is what all of this exists to guard against:
    // today s[0:2] splits the e-acute and yields invalid UTF-8.
    if (utf8.is_valid_utf8(s[0 : 2].to_owned())) { return 25; }
    return 0;
}
`

func TestCharBoundaryInterp(t *testing.T) {
	if got := runInterpExit(t, charBoundaryProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestCharBoundaryX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, charBoundaryProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestCharBoundaryWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, charBoundaryProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestCharBoundaryArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, charBoundaryProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
