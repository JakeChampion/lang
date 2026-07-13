package e2e

import "testing"

// Differential coverage for std/option.unzip across backends: Some of a
// pair splits into a pair of Somes, None splits into a pair of Nones,
// and the round-trip with zip. Uses a mixed (i32, string) pair to
// exercise both a scalar and a heap payload. Returns 42 iff every check
// holds. Each leg skips itself when its toolchain is absent.
const optionUnzipProg = `
import "std/option";
function main(): i32 {
    var some: Option[(i32, string)] = Some((7, "hi"));
    var none: Option[(i32, string)] = None;
    var sa: (Option[i32], Option[string]) = some.unzip();
    var na: (Option[i32], Option[string]) = none.unzip();
    // Some((7,"hi")) -> (Some(7), Some("hi")).
    match (sa.0) { Some(v) => { if (v != 7) { return 1; } }, None => { return 2; } }
    match (sa.1) { Some(v) => { if (v != "hi") { return 3; } }, None => { return 4; } }
    // None -> (None, None).
    match (na.0) { Some(v) => { return 5; }, None => {} }
    match (na.1) { Some(v) => { return 6; }, None => {} }
    // Round-trip with zip: unzip then zip recovers Some((7,"hi")).
    var rezipped: Option[(i32, string)] = sa.0.zip(sa.1);
    match (rezipped) {
        Some(p) => { if (p.0 != 7 || p.1 != "hi") { return 7; } },
        None => { return 8; }
    }
    return 42;
}
`

func TestOptionUnzipInterp(t *testing.T) {
	if got := runInterpExit(t, optionUnzipProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestOptionUnzipX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, optionUnzipProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestOptionUnzipWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, optionUnzipProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestOptionUnzipArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, optionUnzipProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
