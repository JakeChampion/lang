package e2e

import "testing"

const stringCountMatchesProg = `
import "std/string";
function main(): i32 {
    if ("aaaa".count("a") != 4) { return 1; }
    if ("aaaa".count("aa") != 2) { return 2; }     // non-overlapping
    if ("aaaaa".count("aa") != 2) { return 3; }    // last "a" left over
    if ("a.b.c.d".count(".") != 3) { return 4; }
    if ("hello".count("z") != 0) { return 5; }
    if ("hello".count("") != 0) { return 6; }      // empty needle
    if ("".count("a") != 0) { return 7; }
    if ("abcabc".count("abc") != 2) { return 8; }
    // Agrees with find_all().len().
    if ("mississippi".count("ss") != "mississippi".find_all("ss").len()) { return 9; }
    return 42;
}
`

func TestStringCountMatchesInterp(t *testing.T) {
	if got := runInterpExit(t, stringCountMatchesProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStringCountMatchesX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringCountMatchesProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStringCountMatchesWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringCountMatchesProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestStringCountMatchesArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringCountMatchesProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
