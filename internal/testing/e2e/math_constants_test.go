package e2e

import "testing"

const mathConstantsProg = `
import "std/math";
function main(): i32 {
    // TAU is exactly two PI (scaling an f64 by 2 only bumps the exponent).
    if (math.TAU != 2.0 * math.PI) { return 6; }
    // PI sits in the tight interval around the true value.
    if (math.PI <= 3.141592653 || math.PI >= 3.141592654) { return 7; }
    return 42;
}
`

func TestMathConstantsInterp(t *testing.T) {
	if got := runInterpExit(t, mathConstantsProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMathConstantsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mathConstantsProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestMathConstantsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mathConstantsProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestMathConstantsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mathConstantsProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
