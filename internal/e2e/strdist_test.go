package e2e

import "testing"

// Differential coverage for std/strdist across backends: Levenshtein
// reference distances, code-point awareness (é vs e = 1), and the
// similarity ratio bounds. Returns 42 iff every check holds. Each leg
// skips itself when its toolchain is absent.
const strdistProg = `
import "std/strdist" as strdist;
function main(): i32 {
    if (strdist.levenshtein("kitten", "sitting") != 3) { return 1; }
    if (strdist.levenshtein("flaw", "lawn") != 2) { return 2; }
    if (strdist.levenshtein("book", "back") != 2) { return 3; }
    if (strdist.levenshtein("a", "") != 1 || strdist.levenshtein("", "abc") != 3) { return 4; }
    if (strdist.levenshtein("café", "cafe") != 1) { return 5; }
    if (strdist.similarity("abc", "abc") != 1.0 || strdist.similarity("", "") != 1.0) { return 6; }
    if (strdist.similarity("abc", "xyz") != 0.0) { return 7; }
    var s: f64 = strdist.similarity("kitten", "sitting");
    if (s < 0.57 || s > 0.58) { return 8; }
    return 42;
}
`

func TestStrdistInterp(t *testing.T) {
	if got := runInterpExit(t, strdistProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStrdistX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, strdistProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStrdistWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, strdistProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestStrdistArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, strdistProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
