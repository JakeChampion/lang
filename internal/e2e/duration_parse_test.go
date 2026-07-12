package e2e

import "testing"

// Differential coverage for std/format.parse_duration_ms across backends:
// single units (ms/s/m/h/d), multi-part durations with and without
// separating spaces, the i64 range beyond i32, and the None rejections
// (empty, bare number, unknown unit, unit with no number). Returns 42 iff
// every check holds. Each leg skips itself when its toolchain is absent.
const durationParseProg = `
import "std/format" as fmt;
function chk(s: string, want: i64): boolean {
    match (fmt.parse_duration_ms(s)) {
        Some(v) => { return v == want; },
        None => { return false; },
    }
}
function isnone(s: string): boolean {
    match (fmt.parse_duration_ms(s)) {
        Some(v) => { return false; },
        None => { return true; },
    }
}
function main(): i32 {
    if (!chk("500ms", 500 as i64)) { return 1; }
    if (!chk("1s", 1000 as i64)) { return 2; }
    if (!chk("1m", 60000 as i64)) { return 3; }
    if (!chk("1h", 3600000 as i64)) { return 4; }
    if (!chk("2d", 172800000 as i64)) { return 5; }
    if (!chk("1h30m", 5400000 as i64)) { return 6; }
    if (!chk("1h 30m", 5400000 as i64)) { return 7; }
    if (!chk("1h30m45s500ms", 5445500 as i64)) { return 8; }
    if (!chk("1h 2m 3s 4ms", 3723004 as i64)) { return 9; }
    // Beyond i32 range: 30 days in ms = 2592000000 > 2^31.
    if (!chk("30d", 2592000000 as i64)) { return 10; }
    if (!isnone("")) { return 11; }
    if (!isnone("5")) { return 12; }
    if (!isnone("5x")) { return 13; }
    if (!isnone("h")) { return 14; }
    return 42;
}
`

func TestDurationParseInterp(t *testing.T) {
	if got := runInterpExit(t, durationParseProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestDurationParseX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, durationParseProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestDurationParseWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, durationParseProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestDurationParseArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, durationParseProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
