package e2e

import "testing"

const floatSignProg = `
import "std/f64";
function main(): i32 {
    let negzero: f64 = f64_from_bits(0 - 9223372036854775807 - 1);   // 0x8000...0
    if (!(0.0 - 5.0).is_sign_negative()) { return 1; }
    if ((5.0).is_sign_negative()) { return 2; }
    if (!negzero.is_sign_negative()) { return 3; }         // -0.0 is negative
    if ((0.0).is_sign_negative()) { return 4; }            // +0.0 is not
    if (!(5.0).is_sign_positive()) { return 5; }
    if ((0.0 - 5.0).is_sign_positive()) { return 6; }
    if (!(0.0).is_sign_positive()) { return 7; }
    if (negzero.is_sign_positive()) { return 8; }
    let inf: f64 = 1.0 / 0.0;
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
