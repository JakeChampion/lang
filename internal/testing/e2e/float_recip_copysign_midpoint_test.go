package e2e

import "testing"

const floatRecipCopysignMidpointProg = `
import "std/f64";
function approx(a: f64, b: f64): boolean { let d: f64 = a - b; if (d < 0.0) { d = 0.0 - d; } return d < 0.0001; }
function main(): i32 {
    if (!approx((4.0).recip(), 0.25)) { return 1; }
    if (!approx((2.0).recip(), 0.5)) { return 2; }
    if (!approx((0.0 - 8.0).recip(), 0.0 - 0.125)) { return 3; }         // sign carries
    if (!approx((3.0).copysign(0.0 - 1.0), 0.0 - 3.0)) { return 4; }     // + magnitude, - sign
    if (!approx((0.0 - 3.0).copysign(2.0), 3.0)) { return 5; }           // - magnitude, + sign
    if (!approx((5.0).copysign(0.0), 5.0)) { return 6; }                 // zero reads positive
    if (!approx((2.0).midpoint(4.0), 3.0)) { return 7; }
    if (!approx((1.0).midpoint(2.0), 1.5)) { return 8; }
    if (!approx((0.0 - 4.0).midpoint(4.0), 0.0)) { return 9; }           // straddling zero
    // recip round-trips: recip(recip(x)) == x
    if (!approx((7.0).recip().recip(), 7.0)) { return 10; }
    // f32 mirrors
    if (!approx((4.0 as f32).recip() as f64, 0.25)) { return 11; }
    if (!approx((0.0 - 3.0 as f32).copysign(1.0 as f32) as f64, 3.0)) { return 12; }
    if (!approx((10.0 as f32).midpoint(20.0 as f32) as f64, 15.0)) { return 13; }
    return 42;
}
`

func TestFloatRecipCopysignMidpointInterp(t *testing.T) {
	if got := runInterpExit(t, floatRecipCopysignMidpointProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestFloatRecipCopysignMidpointX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, floatRecipCopysignMidpointProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestFloatRecipCopysignMidpointWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, floatRecipCopysignMidpointProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestFloatRecipCopysignMidpointArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, floatRecipCopysignMidpointProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
