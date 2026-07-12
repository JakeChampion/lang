package e2e

import "testing"

// Differential coverage for std/math.parse_rgb_hex across backends:
// the 6-digit and 3-digit-shorthand forms (with and without a leading
// '#'), case-insensitivity, the pack_rgb round-trip via to_rgb_hex, and
// the None rejections (wrong length, bad digit, empty). Option[i32] (an
// i32-width enum payload) lowers on all four backends. Returns 42 iff
// every check holds. Each leg skips itself when its toolchain is absent.
const parseRgbHexProg = `
import "std/math" as math;
import "std/i32";
function opt(o: Option[i32], fb: i32): i32 {
    match (o) { Some(v) => { return v; }, None => { return fb; } }
}
function main(): i32 {
    if (opt(math.parse_rgb_hex("#ff8800"), -1) != math.pack_rgb(255, 136, 0)) { return 1; }
    if (opt(math.parse_rgb_hex("ff8800"), -1) != math.pack_rgb(255, 136, 0)) { return 2; }
    if (opt(math.parse_rgb_hex("#FF8800"), -1) != math.pack_rgb(255, 136, 0)) { return 3; }
    if (opt(math.parse_rgb_hex("#f80"), -1) != math.pack_rgb(255, 136, 0)) { return 4; }
    if (opt(math.parse_rgb_hex("f80"), -1) != math.pack_rgb(255, 136, 0)) { return 5; }
    if (opt(math.parse_rgb_hex("#000000"), -1) != 0) { return 6; }
    if (opt(math.parse_rgb_hex("#ffffff"), -1) != math.pack_rgb(255, 255, 255)) { return 7; }
    if (math.pack_rgb(18, 52, 86).to_rgb_hex() != "#123456") { return 8; }
    if (opt(math.parse_rgb_hex("#123456"), -1) != math.pack_rgb(18, 52, 86)) { return 9; }
    if (opt(math.parse_rgb_hex("#12345"), -99) != -99) { return 10; }
    if (opt(math.parse_rgb_hex("#gg0000"), -99) != -99) { return 11; }
    if (opt(math.parse_rgb_hex(""), -99) != -99) { return 12; }
    if (opt(math.parse_rgb_hex("#"), -99) != -99) { return 13; }
    return 42;
}
`

func TestParseRgbHexInterp(t *testing.T) {
	if got := runInterpExit(t, parseRgbHexProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestParseRgbHexX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, parseRgbHexProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestParseRgbHexWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, parseRgbHexProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestParseRgbHexArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, parseRgbHexProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
