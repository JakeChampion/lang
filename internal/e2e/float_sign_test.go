package e2e

// Differential coverage for the float sign-bit predicates `is_sign_negative` /
// `is_sign_positive` on std/float (f64 + f32). They read the IEEE-754 sign bit
// (via f64_bits / f32_bits), so they classify `-0.0` as negative and `+0.0` as
// positive — which `x < 0.0` cannot (it is false for both zeros and for NaN).
// Checks the ordinary signs, both signed zeros (constructed from bits so the
// literal `0.0 - 0.0 == +0.0` folding can't hide the case), and the infinities.
// Returns 42 iff every check holds across interp / x86-64 / wasm / arm64.

import "testing"

const floatSignProg = `
import "std/float";
function main(): i32 {
    var negzero: f64 = f64_from_bits(0 - 9223372036854775807 - 1);   // 0x8000...0
    if (!(0.0 - 5.0).is_sign_negative()) { return 1; }
    if ((5.0).is_sign_negative()) { return 2; }
    if (!negzero.is_sign_negative()) { return 3; }         // -0.0 is negative
    if ((0.0).is_sign_negative()) { return 4; }            // +0.0 is not
    if (!(5.0).is_sign_positive()) { return 5; }
    if ((0.0 - 5.0).is_sign_positive()) { return 6; }
    if (!(0.0).is_sign_positive()) { return 7; }
    if (negzero.is_sign_positive()) { return 8; }
    var inf: f64 = 1.0 / 0.0;
    if (!inf.is_sign_positive()) { return 9; }
    if (!(0.0 - inf).is_sign_negative()) { return 10; }
    // f32 siblings.
    if (!(0.0 as f32 - 3.0 as f32).is_sign_negative()) { return 20; }
    if ((3.0 as f32).is_sign_negative()) { return 21; }
    if (!(3.0 as f32).is_sign_positive()) { return 22; }
    return 42;
}
`

func TestFloatSignInterp(t *testing.T) {
	if got := runInterpExit(t, floatSignProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestFloatSignX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, floatSignProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestFloatSignWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, floatSignProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestFloatSignArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, floatSignProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
