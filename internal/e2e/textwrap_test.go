package e2e

import "testing"

// Differential coverage for std/textwrap.word_wrap across backends:
// greedy wrap, the exact-fit boundary, a long unbreakable word, hard
// newlines / blank lines, space collapsing, and edge widths. Returns 42
// iff every exact-string check holds. Each leg skips itself when its
// toolchain is absent.
const textwrapProg = `
import "std/textwrap" as tw;
function main(): i32 {
    if (tw.word_wrap("the quick brown fox", 10) != "the quick\nbrown fox") { return 1; }
    if (tw.word_wrap("the quick brown fox", 100) != "the quick brown fox") { return 2; }
    if (tw.word_wrap("supercalifragilistic hi", 5) != "supercalifragilistic\nhi") { return 3; }
    if (tw.word_wrap("a b\n\nc d", 10) != "a b\n\nc d") { return 4; }
    if (tw.word_wrap("a   b", 10) != "a b") { return 5; }
    if (tw.word_wrap("aa bb", 5) != "aa bb" || tw.word_wrap("aa bb", 4) != "aa\nbb") { return 6; }
    if (tw.word_wrap("", 10) != "" || tw.word_wrap("a b c", 0) != "a b c") { return 7; }
    return 42;
}
`

func TestTextwrapInterp(t *testing.T) {
	if got := runInterpExit(t, textwrapProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestTextwrapX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, textwrapProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestTextwrapWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, textwrapProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestTextwrapArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, textwrapProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
