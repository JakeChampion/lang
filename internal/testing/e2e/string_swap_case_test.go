package e2e

import "testing"

const stringSwapCaseProg = `
import "std/string";
function main(): i32 {
    if ("Hello World".swap_case() != "hELLO wORLD") { return 1; }
    if ("abc".swap_case() != "ABC") { return 2; }
    if ("ABC".swap_case() != "abc") { return 3; }
    if ("123!@#".swap_case() != "123!@#") { return 4; }   // non-letters unchanged
    if ("".swap_case() != "") { return 5; }               // empty
    if ("MixedCase123".swap_case() != "mIXEDcASE123") { return 6; }
    // involution: swapping twice recovers the original
    if ("Hello, World!".swap_case().swap_case() != "Hello, World!") { return 7; }
    return 42;
}
`

func TestStringSwapCaseInterp(t *testing.T) {
	if got := runInterpExit(t, stringSwapCaseProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStringSwapCaseX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringSwapCaseProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStringSwapCaseWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringSwapCaseProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestStringSwapCaseArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringSwapCaseProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
